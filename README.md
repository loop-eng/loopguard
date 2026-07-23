# LoopGuard

[![CI](https://github.com/loop-eng/loopguard/actions/workflows/ci.yaml/badge.svg)](https://github.com/loop-eng/loopguard/actions/workflows/ci.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/loop-eng/loopguard)](https://goreportcard.com/report/github.com/loop-eng/loopguard)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Circuit breaker daemon for AI agent loops.** Monitors Claude Code, Codex, and Gemini CLI sessions in real-time — detects runaway loops, enforces hard budget limits, and pauses offending processes before they drain your wallet.

## The Problem

AI coding agents can spin for hours burning tokens on unproductive loops:

| Incident | Cost | Root Cause |
|----------|------|------------|
| Enterprise single-month | $500M on Claude | No usage limits deployed |
| Overnight agent run | $437 | 14,000 redundant tool calls |
| LangChain 4-agent loop | $47K over 11 days | Infinite loop, nobody noticed |

Claude Code's `--max-budget-usd` [overshoots by 8x](https://github.com/anthropics/claude-code/issues/4277). Built-in [loop detection](https://github.com/anthropics/claude-code/issues/4277) is unimplemented. Cursor has zero loop detection.

## What LoopGuard Does

LoopGuard sits beside your agent as a circuit breaker:

- **Budget enforcement** — hard pause at $20/session (configurable)
- **Spin detection** — catches repeated tool calls, error echoes, no-progress stalls
- **Cost velocity alerts** — warns when burn rate exceeds $2/min
- **Multi-agent** — monitors Claude Code, Codex, Gemini CLI, and custom sources simultaneously
- **Zero config** — works immediately after install with sane defaults
- **Process-safe** — SIGSTOP/SIGCONT preserves agent state perfectly
- **LTF traces** — emits [Loop Trace Format](https://github.com/loop-eng/ltf) events for every intervention

## Install

```bash
# Homebrew (macOS/Linux)
brew install loop-eng/tap/loopguard

# Go install
go install github.com/loop-eng/loopguard@latest

# Binary download — see Releases
```

## Quick Start

```bash
# Run in foreground (auto-discovers all active sessions)
loopguard

# Or install as auto-start service
loopguard install
loopguard start

# Check what's being monitored
loopguard status

# Resume a paused session
loopguard resume <session-id>
```

That's it. No config file needed. LoopGuard auto-discovers Claude Code sessions in `~/.claude/projects/` and starts watching them with default budgets.

## How It Works

```
Agent Session (JSONL logs)
    │
    ▼
┌────────────┐    ┌───────────┐    ┌────────────────┐
│  Discovery │───▶│  Watcher  │───▶│    Analyzer     │
│  (auto-    │    │  (fsnotify│    │  ┌────────────┐ │
│   detect)  │    │  + poll)  │    │  │ Cost Calc  │ │
└────────────┘    └───────────┘    │  │ Spin Detect│ │
                                   │  │ Budget     │ │
                                   │  └─────┬──────┘ │
                                   └────────┼────────┘
                                            │
                              ┌─────────────▼──────────────┐
                              │         Enforcer           │
                              │  SIGSTOP + notify + trace  │
                              └────────────────────────────┘
```

1. **Discovery** — Scans `~/.claude/projects/`, `~/.codex/sessions/`, and Gemini CLI's `~/.gemini/tmp/` (or `$GEMINI_DATA_DIR/tmp/`) for active session files
2. **Watcher** — Tails JSONL files via fsnotify with 100ms debounce + 5s polling fallback
3. **Analyzer** — Calculates real-time cost from token usage, detects spin patterns, enforces budgets
4. **Enforcer** — Pauses the agent process via SIGSTOP, sends a desktop notification, writes an LTF trace event

## Configuration

LoopGuard works with zero configuration. To customize:

```bash
loopguard config init    # creates ~/.config/loopguard/config.yaml
```

### Full Config Reference

```yaml
budget:
  per_session_usd: 20.0      # pause session after this cost
  per_hour_usd: 50.0         # hourly cap across all sessions
  per_day_usd: 200.0         # daily cap
  warn_at_percent: 80        # warn at this % of any limit

spin_detection:
  repeated_calls: 3           # same tool call N times → spin
  error_echo: 3               # same error N times → spin
  stall_minutes: 10           # no file changes for N min → warn
  cost_velocity_per_min: 2.0  # $/min threshold
  context_fill_percent: 85    # context window fill % → spin (0 disables)

enforcement:
  action: pause               # pause | kill | warn
  sentinel_fallback: true     # write .loopguard-stop if SIGSTOP fails

notifications:
  desktop: true
  sound: true

sources:
  claude_code: auto           # auto | disabled
  codex: auto                 # auto | disabled
  gemini: auto                # auto | disabled
  custom: []                  # additional glob patterns to watch

traces:
  enabled: true
  output_dir: ~/.config/loopguard/traces/

logging:
  level: info                 # debug | info | warn | error
  file: ~/.config/loopguard/loopguard.log
```

### Environment Variable Overrides

Environment variables take precedence over the config file:

```bash
export LOOPGUARD_BUDGET_PER_SESSION=50
export LOOPGUARD_BUDGET_PER_HOUR=100
export LOOPGUARD_BUDGET_PER_DAY=500
export LOOPGUARD_LOG_LEVEL=debug
```

## CLI Reference

| Command | Description |
|---------|-------------|
| `loopguard` | Start daemon in foreground |
| `loopguard status` | Show active sessions with cost and status |
| `loopguard status --json` | Output session data as JSON |
| `loopguard resume <id>` | Resume a paused session (prefix match) |
| `loopguard config` | Show config file location |
| `loopguard config init` | Create default config file |
| `loopguard install` | Install as system service (launchd/systemd) |
| `loopguard uninstall` | Remove system service |
| `loopguard start` | Start background service |
| `loopguard stop` | Stop background service |

## Spin Detection

LoopGuard uses five independent heuristics to detect unproductive loops:

| Heuristic | What It Catches | Default Threshold |
|-----------|----------------|-------------------|
| **Repeated tool calls** | Same tool+args fingerprint in recent history | 3 repeats |
| **Error echo** | Same error message repeating | 3 identical errors |
| **No-progress stall** | Token spend without file modifications | 10 minutes |
| **Cost velocity** | Burn rate exceeding $/min threshold | $2.00/min |
| **Context bloat** | Context window filling up (estimated from `input_tokens` vs. the model's known context window) | 85% full |

## Supported Models (Pricing)

LoopGuard has embedded pricing for accurate cost calculation:

| Provider | Models |
|----------|--------|
| **Anthropic** | Opus 4.8/4.7/4.6, Sonnet 4.6/4.5, Haiku 4.5 |
| **OpenAI** | GPT-5.5, GPT-4.1, GPT-4.1-mini, o4-mini, o3 |
| **Google** | Gemini 2.5 Pro, Gemini 2.5 Flash |

Unknown models fall back to Sonnet-tier pricing.

## Competitive Landscape

| Tool | Real-time | Budget Kill | Spin Detect | Multi-Agent | Zero Config |
|------|-----------|-------------|-------------|-------------|-------------|
| **LoopGuard** | Yes | Yes | Yes | Yes | Yes |
| tokscale | Yes | No | No | Yes | No |
| ccusage | No | No | No | No | Yes |
| Claude built-in | Partial | No | No | No | Yes |
| Codex built-in | Partial | Partial | No | No | Yes |

## LTF Traces

Every intervention is recorded as an [LTF](https://github.com/loop-eng/ltf) (Loop Trace Format) event:

```jsonl
{"ltf_version":"1.0","loop_id":"sess-abc","timestamp":"2026-07-07T14:30:00Z","phase":"terminate","action":{"type":"circuit_breaker","detail":"budget_exceeded"},"cost_usd":20.12,"metadata":{"source":"loopguard","action":"paused"}}
```

Traces are written to `~/.config/loopguard/traces/<session-id>.ltf.jsonl` and can be consumed by other loop-eng tools.

## Part of loop-eng

LoopGuard is the flagship tool in the [loop-eng](https://github.com/loop-eng) ecosystem — developer tools for the nascent field of loop engineering. All tools share the [LTF](https://github.com/loop-eng/ltf) format:

| Tool | Purpose |
|------|---------|
| **LoopGuard** | Circuit breaker daemon (this repo) |
| **LTF** | Loop Trace Format specification |
| **LoopCtl** | TUI dashboard for session monitoring |
| **Kit** | Loop scaffolding CLI |
| **Loop-Bench** | Loop design benchmarking |
| **LoopReplay** | Session step-through debugger |

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for build instructions, testing guide, and how to add support for new agent sources.

See [CHANGELOG.md](CHANGELOG.md) for release history.

```bash
make build      # build binary to bin/loopguard
make test       # run tests with race detector
make lint       # run golangci-lint
make run        # build and run
make clean      # remove build artifacts
```

## License

MIT
