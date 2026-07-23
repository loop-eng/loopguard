# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] - 2026-07-24

Initial public release. Circuit breaker daemon for AI agent loops.

### Added

- **Session discovery** — auto-detects Claude Code sessions in `~/.claude/projects/`, Codex sessions in `~/.codex/sessions/`, and Gemini CLI sessions in `~/.gemini/tmp/` (or `$GEMINI_DATA_DIR/tmp/`); custom JSONL paths via config glob patterns
- **Real-time file watching** — fsnotify with 100ms debounce and 5s polling fallback; offset-tracking tailer for efficient incremental reads; whole-file mode for legacy Gemini CLI JSON files
- **JSONL parsers** — Claude Code, Codex, and Gemini CLI parsers with requestId deduplication, handling malformed/oversized lines gracefully; Gemini parser supports both legacy monolithic JSON and current append-only JSONL formats
- **Cost calculation** — 13-model embedded pricing table (Anthropic, OpenAI, Google) with longest-prefix matching; unknown models fall back to Sonnet-tier pricing; user-configurable pricing overrides via `pricing:` config section
- **Budget enforcement** — per-session, per-hour, and per-day limits with configurable warning threshold (default 80%); hot-reloadable at runtime
- **Spin detection** — five independent heuristics: repeated tool calls, error echo, no-progress stall, cost velocity, context window bloat (estimated from `input_tokens` vs. model's known context window size)
- **Context bloat detection** — estimates context window fill percentage from input token counts; triggers at configurable threshold (default 85%); knows context windows for all major models with conservative fallback for unrecognized models
- **Process control** — SIGSTOP/SIGCONT via process groups with self-signal protection; sentinel file fallback when signals are unavailable
- **Kill API** — `POST /api/sessions/{id}/kill` endpoint + `loopguard kill <prefix>` CLI command for terminating sessions (SIGTERM → SIGKILL escalation)
- **Session detail API** — `GET /api/sessions/{id}` returns PID, log path, cost, last event
- **Config API** — `GET /api/config` returns running configuration as JSON; `loopguard config show` CLI command
- **Config hot-reload** — daemon watches `config.yaml` via fsnotify; validates changes, swaps atomically, and propagates to all sub-components (budget, spin detection, notifications, pricing) without restart
- **Crashed session detection** — 15-second reaper goroutine detects when paused processes die externally (OOM, kill -9, reboot); transitions to terminated state with desktop notification
- **Resume** — `loopguard resume <prefix>` with ambiguous-prefix detection; rejects terminated sessions with clear error messages
- **Desktop notifications** — macOS (osascript) and Linux (notify-send) with sound and urgency levels; hot-reloadable enable/disable
- **Log rotation** — lumberjack-based rotation with configurable max size (50 MB), max backups (3), max age (30 days), and gzip compression
- **LTF traces** — per-session JSONL trace files in Loop Trace Format with intervention events and session summaries
- **History** — append-only JSONL history log of all interventions
- **IPC API** — Unix domain socket HTTP server (7 endpoints) for CLI-to-daemon communication with graceful shutdown
- **CLI** — `status`, `resume`, `kill`, `config` (init/show), `install`/`uninstall`/`start`/`stop` commands via Cobra
- **Service management** — launchd (macOS) and systemd (Linux) integration via kardianos/service
- **Configuration** — YAML config file with `config init`; environment variable overrides (env wins); zero-config defaults; runtime validation
- **Logging** — dual output (stderr + rotating file), configurable level (debug/info/warn/error)
- **Distribution** — GoReleaser for macOS/Linux arm64/amd64 binaries + Homebrew tap (`loop-eng/tap/loopguard`); `go install` version fallback via debug.ReadBuildInfo

### Security

- Sentinel file writes reject symlinks (Lstat check + O_NOFOLLOW)
- Custom paths restricted to user home directory; rejected when HOME is unset
- History/emitter files opened with O_NOFOLLOW to prevent TOCTOU symlink attacks
- HTTP server has read/write/idle timeouts (10s/10s/60s)
- Process group self-signal detection prevents daemon from freezing itself
- PID reuse protection in kill follow-up (process start time validation)
- Log file symlink check prevents writing to attacker-controlled paths

### Testing

- 94+ Go unit and integration tests, all passing with race detector
- 5 fuzz targets (Claude parser, Codex parser, Gemini parser, tailer, whole-file tailer) — zero panics
- 26 automated E2E tests covering all detection heuristics, enforcement, resume, config, and error handling
- Adversarial testing: binary garbage, empty files, 2MB lines, partial JSON, negative/zero/huge config values, rapid start/stop cycles
- Live enforcement verified: real SIGSTOP/SIGCONT on running processes
- Real Claude Code, Codex, and Gemini CLI session cost tracking verified
- golangci-lint clean (errcheck, govet, staticcheck, unused, ineffassign)

[1.0.0]: https://github.com/loop-eng/loopguard/releases/tag/v1.0.0
