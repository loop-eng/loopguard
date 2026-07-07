# LoopGuard — Technical Architecture

## System Overview

LoopGuard is a background daemon that monitors AI agent session logs in real-time, detects runaway behavior, enforces hard budget limits, and pauses offending processes — all with zero configuration required.

```
┌─────────────────────────────────────────────────────────────────┐
│  loopguard daemon (single Go binary)                            │
│                                                                 │
│  ┌────────────┐    ┌────────────┐    ┌────────────┐            │
│  │  Discovery  │───▶│  Watcher   │───▶│  Analyzer  │            │
│  │  (find      │    │  (fsnotify │    │  (spin     │            │
│  │   sessions) │    │   + poll)  │    │   detect,  │            │
│  └────────────┘    └────────────┘    │   budget)  │            │
│                                      └─────┬──────┘            │
│                                            │                    │
│                              ┌─────────────▼──────────────┐    │
│                              │       Enforcer             │    │
│                              │  (pause / notify / log)    │    │
│                              └─────────────┬──────────────┘    │
│                                            │                    │
│  ┌────────────┐    ┌────────────┐    ┌─────▼──────┐            │
│  │  IPC API   │    │  Config    │    │  LTF       │            │
│  │  (Unix     │    │  (viper    │    │  Emitter   │            │
│  │   socket)  │    │   YAML)    │    │  (traces)  │            │
│  └────────────┘    └────────────┘    └────────────┘            │
│                                                                 │
│  CLI (same binary): loopguard status / resume / config          │
└─────────────────────────────────────────────────────────────────┘
```

## Component Architecture

### 1. Discovery Layer

Responsible for finding active agent sessions across tools.

**Auto-discovery paths:**

| Agent | Session Path | Format |
|-------|-------------|--------|
| Claude Code | `~/.claude/projects/<encoded-path>/<session>.jsonl` | JSONL |
| Codex CLI | `~/.codex/sessions/<session-id>/*.jsonl` | JSONL |
| Gemini CLI | `$GEMINI_CLI_HOME/tmp/*/chats/*.json` | JSON |
| Custom | User-configured paths in `config.yaml` | Configurable |

**Discovery algorithm:**
1. On startup, walk known base directories (`~/.claude/projects/`, `~/.codex/sessions/`)
2. Find all `.jsonl` files modified in last 24 hours
3. For each, check if a corresponding process is still running (PID detection via `pgrep -f`)
4. Mark as `active` (process running) or `recent` (completed within 24h)
5. Register fsnotify watchers on parent directories to catch new sessions
6. Re-scan every 60s as fallback for any missed fsnotify events

**Implementation:**
```go
type SessionSource struct {
    Agent     string // "claude", "codex", "gemini"
    BasePath  string // e.g., "~/.claude/projects/"
    Pattern   string // glob pattern for session files
    Format    string // "jsonl" or "json"
}

type DiscoveredSession struct {
    ID        string
    Agent     string
    Path      string
    ProjectDir string
    PID       int    // 0 if process not found
    Active    bool
    StartedAt time.Time
    LastEvent time.Time
}
```

### 2. Watcher Layer

Monitors session files for new events using fsnotify + polling fallback.

**fsnotify setup:**
- Watch parent directories (not individual files) — fsnotify works at directory level
- Filter events to only process `Write` operations on `.jsonl` files
- Debounce: 100ms window to coalesce rapid writes (Claude Code streams multiple lines per response)
- When a new directory is created under a watched path, add a new watcher (handles new sessions)

**Polling fallback:**
- Every 5s, stat all watched files for size changes
- Catches events missed by fsnotify (known issue on some macOS configurations)
- Uses `os.Stat().Size()` comparison — cheaper than re-reading files

**JSONL tail reading:**
- Track file offset per session (last byte position read)
- On each event, seek to last offset, read new lines
- Parse each complete line as JSON
- Handle partial lines (buffer incomplete lines until newline)
- Handle file truncation (reset offset to 0)

```go
type FileTracker struct {
    Path       string
    Offset     int64
    PartialBuf []byte
    LastRead   time.Time
}

func (ft *FileTracker) ReadNewLines() ([]json.RawMessage, error) {
    // Open file, seek to offset, read new content
    // Split on newlines, parse complete JSON lines
    // Buffer incomplete last line
    // Update offset
}
```

### 3. Analyzer Layer

Processes parsed events and detects problems. This is the brain of LoopGuard.

#### 3a. Cost Calculator

Calculates real-time cost from token usage in session logs.

```go
type CostCalculator struct {
    pricing map[string]ModelPricing // keyed by model name
}

type ModelPricing struct {
    InputPerMTok        float64
    OutputPerMTok       float64
    CacheReadPerMTok    float64
    CacheWritePerMTok   float64
}

func (cc *CostCalculator) Calculate(usage TokenUsage, model string) float64 {
    p := cc.pricing[model]
    cost := float64(usage.InputTokens) * p.InputPerMTok / 1_000_000
    cost += float64(usage.OutputTokens) * p.OutputPerMTok / 1_000_000
    cost += float64(usage.CacheReadTokens) * p.CacheReadPerMTok / 1_000_000
    cost += float64(usage.CacheWriteTokens) * p.CacheWritePerMTok / 1_000_000
    return cost
}
```

**Pricing data source:** Embedded pricing table updated at release time. Structure allows user override via config.

#### 3b. Spin Detector

Five independent heuristics, each producing a score. Combined score triggers intervention.

| Heuristic | Detection Logic | Default Threshold |
|-----------|----------------|-------------------|
| **Repeated tool calls** | Same `(tool_name, args_hash)` appearing N times in last M events | 3 repeats in last 10 events |
| **Error echo** | Same error message substring appearing N times without a different action between them | 3 identical errors |
| **No-progress stall** | No file modifications detected (no `Write`, `Edit` tool calls with different targets) for N minutes despite ongoing token spend | 10 minutes |
| **Context bloat** | Context window fill % exceeding threshold based on estimated token accumulation | 85% of model's context window |
| **Cost velocity** | Token spend rate exceeding $/minute threshold (rolling 5-minute average) | $2.00/minute |

```go
type SpinDetector struct {
    recentEvents  []ParsedEvent // circular buffer, last 50 events
    errorHistory  []string      // recent error messages
    lastFileEdit  time.Time     // last file modification
    tokenSpend    []TimedCost   // (timestamp, cost) pairs for velocity calc
}

type SpinResult struct {
    IsSpinning    bool
    Confidence    float64  // 0.0-1.0
    Reasons       []string // human-readable explanations
    Heuristic     string   // which heuristic triggered
}
```

#### 3c. Budget Enforcer

Tracks cumulative cost against configured limits.

```go
type BudgetEnforcer struct {
    perSession  float64 // max cost per session
    perHour     float64 // max cost per hour across all sessions
    perDay      float64 // max cost per day
    
    sessionCosts map[string]float64 // session_id -> cumulative cost
    hourlyCosts  []TimedCost        // sliding window
    dailyCost    float64            // today's total
}

type BudgetResult struct {
    Exceeded     bool
    Limit        string  // "per_session", "per_hour", "per_day"
    Current      float64
    Maximum      float64
    Percentage   float64
}
```

### 4. Enforcer Layer

Takes action when the analyzer detects problems.

**Action hierarchy:**
1. **Warn** (80% budget): Desktop notification, log warning
2. **Pause** (budget exceeded or spin detected): SIGSTOP the agent process, desktop notification, LTF trace event
3. **Kill** (unresponsive to pause or user-configured): SIGTERM → SIGKILL after 5s grace

**Process control strategy:**
- Primary: `syscall.Kill(pid, syscall.SIGSTOP)` — pauses process without losing state
- Resume: `syscall.Kill(pid, syscall.SIGCONT)` — resumes from exact point
- Fallback (if PID unknown): Write `.loopguard-stop` sentinel file in the project directory
- Use process groups (`-pgid`) to pause the entire process tree (agent + child processes)

```go
type Enforcer struct {
    notifier  Notifier
    ltfWriter *LTFWriter
}

type Intervention struct {
    SessionID    string
    Action       string    // "warn", "pause", "kill"
    Reason       string    // human-readable
    Trigger      string    // "budget_exceeded", "spin_detected", "cost_velocity"
    Cost         float64
    Timestamp    time.Time
}

func (e *Enforcer) Pause(session DiscoveredSession, reason string) error {
    // 1. Send SIGSTOP to process group
    pgid, _ := syscall.Getpgid(session.PID)
    if err := syscall.Kill(-pgid, syscall.SIGSTOP); err != nil {
        // Fallback: write sentinel file
        return e.writeSentinel(session.ProjectDir)
    }
    
    // 2. Send desktop notification
    e.notifier.Send("LoopGuard: Agent Paused", reason, UrgencyCritical)
    
    // 3. Write LTF terminate event
    e.ltfWriter.WriteIntervention(session.ID, "pause", reason)
    
    return nil
}
```

### 5. IPC Layer (CLI ↔ Daemon Communication)

Unix domain socket serving a simple JSON-over-HTTP API.

**Socket path:** `~/Library/Application Support/loopguard/loopguard.sock` (macOS) or `$XDG_RUNTIME_DIR/loopguard/loopguard.sock` (Linux)

**API endpoints:**

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/status` | All sessions with current cost, status, alerts |
| GET | `/api/sessions` | List discovered sessions |
| GET | `/api/sessions/:id` | Detail for one session |
| POST | `/api/sessions/:id/resume` | Resume a paused session |
| POST | `/api/sessions/:id/kill` | Kill a session |
| GET | `/api/config` | Current configuration |
| GET | `/api/health` | Daemon health check |

**Single-instance enforcement:**
- On startup, try to connect to existing socket
- If connection succeeds → daemon already running, print message and exit
- If connection fails → remove stale socket file, start daemon
- Register signal handlers for cleanup on SIGTERM/SIGINT

```go
func startDaemon(sockPath string) error {
    // Try connecting to existing instance
    conn, err := net.Dial("unix", sockPath)
    if err == nil {
        conn.Close()
        return fmt.Errorf("daemon already running")
    }
    
    // Clean up stale socket
    os.Remove(sockPath)
    
    // Start listening
    listener, err := net.Listen("unix", sockPath)
    if err != nil {
        return err
    }
    
    // Set permissions (owner-only)
    os.Chmod(sockPath, 0600)
    
    // Cleanup on exit
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
    go func() {
        <-sigCh
        listener.Close()
        os.Remove(sockPath)
        os.Exit(0)
    }()
    
    // Serve HTTP over Unix socket
    mux := http.NewServeMux()
    mux.HandleFunc("/api/status", handleStatus)
    mux.HandleFunc("/api/sessions/", handleSessions)
    // ...
    return http.Serve(listener, mux)
}
```

### 6. Notification System

Cross-platform desktop notifications with platform-specific enhancements.

```go
func notify(title, message string, urgency Urgency) error {
    switch runtime.GOOS {
    case "darwin":
        sound := "Glass"
        if urgency == UrgencyCritical {
            sound = "Basso"
        }
        script := fmt.Sprintf(
            `display notification %q with title %q sound name %q`,
            message, title, sound,
        )
        return exec.Command("osascript", "-e", script).Run()
    case "linux":
        u := "normal"
        if urgency == UrgencyCritical { u = "critical" }
        return exec.Command("notify-send", "-u", u, title, message).Run()
    default:
        return beeep.Notify(title, message, "")
    }
}
```

### 7. Service Management

Using `kardianos/service` for cross-platform daemon installation.

```go
type program struct {
    daemon *Daemon
}

func (p *program) Start(s service.Service) error {
    go p.daemon.Run() // Start must not block
    return nil
}

func (p *program) Stop(s service.Service) error {
    p.daemon.Shutdown()
    return nil
}

// Service config
svcConfig := &service.Config{
    Name:        "loopguard",
    DisplayName: "LoopGuard",
    Description: "Circuit breaker daemon for AI agent loops",
    Option: service.KeyValue{
        "RunAtLoad": true,  // macOS: start on login
    },
}
```

**Generated files:**
- macOS: `~/Library/LaunchAgents/com.loop-eng.loopguard.plist`
- Linux: `~/.config/systemd/user/loopguard.service`

### 8. LTF Trace Emission

Every intervention is recorded as an LTF event for downstream consumption by loopctl, loopreplay, etc.

```go
type LTFEvent struct {
    LTFVersion  string    `json:"ltf_version"`
    LoopID      string    `json:"loop_id"`
    Iteration   int       `json:"iteration"`
    Timestamp   time.Time `json:"timestamp"`
    Phase       string    `json:"phase"` // "terminate" for interventions
    Agent       LTFAgent  `json:"agent"`
    Action      LTFAction `json:"action"`
    Tokens      LTFTokens `json:"tokens"`
    CostUSD     float64   `json:"cost_usd"`
    DurationMs  int64     `json:"duration_ms"`
    Result      LTFResult `json:"result"`
    Metadata    map[string]interface{} `json:"metadata"`
}

// Written to: ~/.config/loopguard/traces/<session-id>.ltf.jsonl
```

## Directory Structure

```
loopguard/
├── cmd/
│   └── loopguard/
│       └── main.go              # Entry point, cobra root command
├── internal/
│   ├── daemon/
│   │   ├── daemon.go            # Main daemon lifecycle
│   │   └── service.go           # kardianos/service integration
│   ├── discovery/
│   │   ├── discovery.go         # Session auto-discovery
│   │   ├── claude.go            # Claude Code session paths
│   │   ├── codex.go             # Codex CLI session paths
│   │   └── gemini.go            # Gemini CLI session paths
│   ├── watcher/
│   │   ├── watcher.go           # fsnotify watcher manager
│   │   ├── tailer.go            # JSONL file tail reader
│   │   └── debounce.go          # Event debouncing
│   ├── parser/
│   │   ├── parser.go            # Interface for session log parsers
│   │   ├── claude.go            # Claude Code JSONL parser
│   │   ├── codex.go             # Codex CLI JSONL parser
│   │   └── event.go             # Normalized event types
│   ├── analyzer/
│   │   ├── analyzer.go          # Main analyzer orchestrator
│   │   ├── cost.go              # Cost calculator
│   │   ├── spin.go              # Spin detection heuristics
│   │   ├── budget.go            # Budget enforcement
│   │   └── pricing.go           # Model pricing table
│   ├── enforcer/
│   │   ├── enforcer.go          # Intervention actions
│   │   ├── process.go           # Process control (SIGSTOP/SIGCONT)
│   │   └── sentinel.go          # Sentinel file fallback
│   ├── notify/
│   │   ├── notify.go            # Cross-platform notifications
│   │   ├── darwin.go            # macOS osascript
│   │   └── linux.go             # Linux notify-send
│   ├── api/
│   │   ├── server.go            # Unix socket HTTP server
│   │   ├── handlers.go          # API endpoint handlers
│   │   └── types.go             # API request/response types
│   ├── ltf/
│   │   ├── writer.go            # LTF trace writer
│   │   └── types.go             # LTF event types
│   └── config/
│       ├── config.go            # Configuration loading
│       └── defaults.go          # Default values
├── go.mod
├── go.sum
├── Makefile
├── .goreleaser.yaml
└── README.md
```

## Configuration Schema

```yaml
# ~/.config/loopguard/config.yaml
# All values are optional — works without any config file

budget:
  per_session_usd: 20.0     # pause session after $20
  per_hour_usd: 50.0        # pause all sessions after $50/hour total
  per_day_usd: 200.0        # daily cap across all sessions
  warn_at_percent: 80       # warn at 80% of any limit

spin_detection:
  repeated_calls: 3          # same tool call N times → spin
  error_echo: 3              # same error N times → spin
  stall_minutes: 10          # no file changes for N minutes → warn
  cost_velocity_per_min: 2.0 # $/minute threshold
  context_fill_percent: 85   # context window fill warning

enforcement:
  action: pause              # "pause" (SIGSTOP) | "kill" (SIGTERM) | "warn" (notify only)
  sentinel_fallback: true    # write .loopguard-stop if SIGSTOP fails

notifications:
  desktop: true
  sound: true                # play sound with notification (macOS only)

sources:
  claude_code: auto          # auto-discover ~/.claude/
  codex: auto                # auto-discover ~/.codex/
  gemini: auto               # auto-discover gemini CLI
  custom: []                 # additional paths to watch

traces:
  enabled: true
  output_dir: ~/.config/loopguard/traces/

logging:
  level: info                # debug | info | warn | error
  file: ~/.config/loopguard/loopguard.log
```

## Data Flow

```
Session files (JSONL)
    │
    ▼
[Discovery] ──── finds sessions ────▶ [Session Registry]
    │                                       │
    ▼                                       │
[Watcher] ──── fsnotify events ────▶ [Tailer] ──── new lines ────▶ [Parser]
                                                                      │
                                                              normalized events
                                                                      │
                                                                      ▼
                                                              ┌──────────────┐
                                                              │   Analyzer   │
                                                              ├──────────────┤
                                                              │ Cost Calc    │──▶ running total
                                                              │ Spin Detect  │──▶ spin score
                                                              │ Budget Check │──▶ limit check
                                                              └──────┬───────┘
                                                                     │
                                                              trigger event
                                                                     │
                                                                     ▼
                                                              ┌──────────────┐
                                                              │   Enforcer   │
                                                              ├──────────────┤
                                                              │ SIGSTOP proc │
                                                              │ Notify user  │
                                                              │ Write LTF    │
                                                              └──────────────┘
```

## Error Handling Strategy

| Scenario | Handling |
|----------|----------|
| Session file deleted mid-watch | Remove from registry, clean up watcher |
| JSONL parse error on a line | Skip line, log warning, continue |
| fsnotify watcher limit hit | Fall back to polling for excess sessions |
| SIGSTOP permission denied | Fall back to sentinel file |
| Agent process not found | Mark session as completed |
| Daemon crash/restart | Re-discover sessions, re-calculate costs from log files |
| Stale socket file | Remove on startup if can't connect |

## Performance Targets

| Metric | Target |
|--------|--------|
| Memory usage | < 30 MB RSS |
| CPU usage (idle) | < 0.1% |
| CPU usage (active, 5 sessions) | < 1% |
| Event processing latency | < 50ms from file write to detection |
| Notification latency | < 200ms from detection to desktop alert |
| Binary size | < 15 MB |
| Startup time | < 500ms |
