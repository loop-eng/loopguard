# LoopGuard — Phase-wise Development Plan

## Milestone Summary

| Phase | Deliverable | Duration | Dependencies |
|-------|------------|----------|-------------|
| **Phase 0** | Project scaffold + CI | 1 day | None |
| **Phase 1** | Core daemon MVP (Claude Code only) | 5 days | Phase 0 |
| **Phase 2** | Multi-source + configuration | 3 days | Phase 1 |
| **Phase 3** | Service installation + LTF | 2 days | Phase 2 |
| **Phase 4** | Polish + distribution | 2 days | Phase 3 |
| **Total** | | **~13 days** | |

---

## Phase 0: Project Scaffold (Day 1)

**Goal:** Repo skeleton, tooling, CI pipeline — all infrastructure before writing business logic.

### Tasks

1. **Initialize Go module**
   - `go mod init github.com/loop-eng/loopguard`
   - Go 1.23+ minimum
   - Add dependencies: cobra, viper, fsnotify, beeep, kardianos/service, slog

2. **Directory structure**
   ```
   loopguard/
   ├── cmd/loopguard/main.go
   ├── internal/{daemon,discovery,watcher,parser,analyzer,enforcer,notify,api,ltf,config}/
   ├── go.mod
   ├── go.sum
   ├── Makefile
   ├── .goreleaser.yaml
   ├── .github/workflows/ci.yaml
   ├── LICENSE (MIT)
   └── README.md
   ```

3. **Makefile targets:** `build`, `test`, `lint`, `run`

4. **GitHub Actions CI:** Go build + test + golangci-lint on push/PR

5. **Cobra CLI scaffold:** root command + `start`, `stop`, `status`, `resume`, `config`, `install`, `uninstall` subcommands (all stubs)

### Exit Criteria
- `make build` produces a binary
- `loopguard --help` shows all subcommands
- CI passes on push

---

## Phase 1: Core Daemon MVP (Days 2-6)

**Goal:** Watch Claude Code sessions, calculate cost, detect spins, enforce budget, send notifications. This is the minimum viable product.

### Day 2: Discovery + Watcher

**Tasks:**
- Implement `internal/discovery/claude.go`: scan `~/.claude/projects/` for `.jsonl` files
- Implement `internal/discovery/discovery.go`: session registry (track path, PID, active status)
- Implement `internal/watcher/watcher.go`: fsnotify setup with directory watching
- Implement `internal/watcher/debounce.go`: per-file timer-based debounce (100ms)
- Implement `internal/watcher/tailer.go`: offset-tracking JSONL tail reader

**Exit criteria:** Running `loopguard` in foreground discovers and tails Claude Code sessions, printing new JSONL lines to stdout.

### Day 3: Parser + Cost Calculator

**Tasks:**
- Implement `internal/parser/event.go`: normalized event types (`ParsedEvent` struct)
- Implement `internal/parser/claude.go`: parse Claude Code JSONL into `ParsedEvent`
  - Extract: message type, tool calls, token usage, model name, timestamp
  - Handle streaming fragments (same UUID = same message, accumulate tokens)
  - Handle malformed lines (skip with warning)
- Implement `internal/analyzer/pricing.go`: embedded model pricing table (Anthropic + OpenAI + Google)
- Implement `internal/analyzer/cost.go`: cost calculator from token usage + model

**Exit criteria:** Running sessions show real-time cumulative cost in logs.

### Day 4: Spin Detection

**Tasks:**
- Implement `internal/analyzer/spin.go`:
  - Repeated tool calls detector (circular buffer of last 50 events, hash comparison)
  - Error echo detector (extract error messages from tool results, track repeats)
  - No-progress stall detector (track last file-modifying tool call timestamp)
  - Cost velocity detector (rolling 5-minute window, $/min calculation)
- Implement `internal/analyzer/analyzer.go`: orchestrator that runs all detectors on each event

**Exit criteria:** Spin detection fires correctly when manually triggering test patterns (e.g., same tool call 3x in test data).

### Day 5: Budget Enforcement + Notifications

**Tasks:**
- Implement `internal/analyzer/budget.go`: per-session, per-hour, per-day budget tracking
- Implement `internal/enforcer/process.go`: find PID via `pgrep`, send SIGSTOP/SIGCONT
- Implement `internal/enforcer/sentinel.go`: write `.loopguard-stop` file as fallback
- Implement `internal/enforcer/enforcer.go`: action hierarchy (warn → pause → kill)
- Implement `internal/notify/notify.go`: cross-platform desktop notification
- Implement `internal/notify/darwin.go`: macOS osascript with sound
- Implement `internal/notify/linux.go`: Linux notify-send

**Exit criteria:** When a test session exceeds $0.10 budget, the process is paused and a desktop notification appears.

### Day 6: IPC API + CLI Commands

**Tasks:**
- Implement `internal/api/server.go`: HTTP over Unix socket
- Implement `internal/api/handlers.go`: `/api/status`, `/api/sessions`, `/api/sessions/:id/resume`
- Wire `loopguard status` CLI to query daemon via Unix socket
- Wire `loopguard resume <session-id>` to send SIGCONT
- Implement single-instance enforcement (try connect on startup)

**Exit criteria:**
- `loopguard` runs as foreground daemon
- `loopguard status` (in another terminal) shows sessions with cost
- `loopguard resume <id>` resumes a paused session
- **MVP is complete and usable**

---

## Phase 2: Multi-Source + Configuration (Days 7-9)

### Day 7: Codex + Gemini Parsers

**Tasks:**
- Implement `internal/discovery/codex.go`: discover `~/.codex/sessions/`
- Implement `internal/parser/codex.go`: parse Codex CLI JSONL events
- Implement `internal/discovery/gemini.go`: discover Gemini CLI sessions
- Update discovery registry to support multiple sources
- Update watcher to handle new source paths

### Day 8: Configuration System

**Tasks:**
- Implement `internal/config/config.go`: viper-based YAML config loading
- Implement `internal/config/defaults.go`: zero-config defaults (works without config)
- `loopguard config` command: create/open config file
- Support all config fields: budget, spin_detection, notifications, sources, logging
- Environment variable overrides (`LOOPGUARD_BUDGET_PER_SESSION`, etc.)

### Day 9: Custom Sources + Logging

**Tasks:**
- Support `custom:` paths in config for arbitrary JSONL sources
- Implement structured logging with `log/slog`
- Log rotation (or log to file with configurable path)
- History tracking: write session summaries to `~/.config/loopguard/history.jsonl`

---

## Phase 3: Service Installation + LTF (Days 10-11)

### Day 10: Daemon Service Management

**Tasks:**
- Implement `internal/daemon/service.go`: kardianos/service integration
- `loopguard install`: create launchd plist (macOS) or systemd user unit (Linux)
- `loopguard uninstall`: remove service
- `loopguard start`: start via service manager
- `loopguard stop`: stop via service manager
- Test auto-start on login (macOS) and boot (Linux)

### Day 11: LTF Trace Emission

**Tasks:**
- Implement `internal/ltf/types.go`: LTF event types matching spec v1.0
- Implement `internal/ltf/writer.go`: write LTF events to trace files
- Emit LTF events for: session start, intervention (warn/pause/kill), session end
- Emit loop_summary event when session completes
- Configurable trace output directory

---

## Phase 4: Polish + Distribution (Days 12-13)

### Day 12: README + Demo

**Tasks:**
- Write comprehensive README with:
  - One-liner and value proposition
  - Animated GIF demo (record with `vhs` or `asciinema`)
  - Installation instructions (Homebrew, `go install`, binary download)
  - Quick start (zero-config usage)
  - Configuration reference
  - How it works (architecture diagram)
  - Competitive comparison table
- Add GitHub topics: `loop-engineering`, `ai-agents`, `circuit-breaker`, `claude-code`, `cost-control`

### Day 13: Distribution

**Tasks:**
- Configure `.goreleaser.yaml`: macOS arm64/amd64, Linux amd64/arm64 binaries
- Create Homebrew tap: `loop-eng/homebrew-tap`
- Homebrew formula: `brew install loop-eng/tap/loopguard`
- GitHub Actions release workflow (tag-triggered)
- Create `go install github.com/loop-eng/loopguard@latest` path
- Post to: Show HN, r/programming, X thread, loop-engineering topic

---

## Post-Launch Roadmap

### Week 3-4: Community Feedback
- Fix issues reported by early adopters
- Add support for Cursor sessions (if log format is documented)
- Add support for Aider sessions

### Month 2: Advanced Features
- TUI status view (mini-dashboard in terminal, reuse bubbletea)
- Slack/Discord webhook notifications
- Rate-based model downgrade suggestions ("You're spending $3/min on Opus — switch to Sonnet?")
- Integration with tokscale (read tokscale data as a source)

### Month 3: Enterprise Features
- Team-level budget aggregation
- Per-project cost attribution
- JSON API for external monitoring integration
- Prometheus metrics endpoint

---

## Risk Registry

| Risk | Impact | Mitigation |
|------|--------|------------|
| Claude Code changes JSONL format | Parser breaks | Version-detect parser, keep backward compat |
| fsnotify misses events on macOS | Sessions unwatched | Polling fallback every 5s |
| SIGSTOP blocked by SIP (macOS) | Can't pause agent | Sentinel file fallback |
| Pricing data goes stale | Cost calculation wrong | Embed with version, allow user override in config |
| User runs as root | Security risk | Refuse to run as root, document why |
| Multiple loopguard instances | Conflict | Socket-based single-instance check |
