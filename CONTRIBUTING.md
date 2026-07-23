# Contributing to LoopGuard

Thank you for your interest in LoopGuard. This project is open to contributions — bug reports, feature requests, code, docs, and testing.

## Quick Start

```bash
# Clone
git clone https://github.com/loop-eng/loopguard.git
cd loopguard

# Build
make build          # outputs bin/loopguard

# Run
make run            # builds and runs in foreground

# Test
make test           # unit + integration tests with race detector
make lint           # golangci-lint (install: https://golangci-lint.run/welcome/install/)
```

### Requirements

- Go 1.24+
- golangci-lint v2.12+ (for `make lint`)
- macOS or Linux (process signals require Unix)

## Running Tests

### Unit & Integration Tests

```bash
go test -race -count=1 ./...
```

Tests cover parsers (Claude, Codex, Gemini), cost calculation, spin detection, context bloat detection, budget enforcement, config loading/validation, LTF output, tailer (including whole-file mode), notifications, API handlers, and daemon lifecycle.

### End-to-End Tests

```bash
# Build the daemon and demo simulator first
make build
go build -o bin/loopguard-demo ./demo/

# Automated suite (26 tests)
bash demo/test_e2e.sh

# Interactive trial (walks through all features in ~30 seconds)
bash demo/trial.sh
```

The E2E suite tests spin detection, error echo, budget enforcement, false positive prevention, concurrent sessions, env var overrides, graceful shutdown, cost tracking, resume flow, stale socket recovery, dual daemon prevention, Codex parsing, and CLI error handling.

### Fuzz Tests

```bash
go test -fuzz=FuzzClaudeParser  -fuzztime=30s ./internal/parser/
go test -fuzz=FuzzCodexParser   -fuzztime=30s ./internal/parser/
go test -fuzz=FuzzGeminiParser  -fuzztime=30s ./internal/parser/
go test -fuzz=FuzzTailer        -fuzztime=30s ./internal/watcher/
go test -fuzz=FuzzTailerWholeFileMode -fuzztime=30s ./internal/watcher/
```

## Project Structure

```
cmd/loopguard/          Entry point
internal/
  analyzer/             Cost calculator, spin detector, budget enforcer, context bloat
  api/                  Unix socket HTTP server + handlers
  cli/                  Cobra commands (root, status, resume, kill, config, install, start, stop)
  config/               YAML + env var config loading, validation, pricing overrides
  daemon/               Main daemon loop, session lifecycle, history, config hot-reload
  discovery/            Session discoverers (Claude, Codex, Gemini CLI, custom)
  enforcer/             SIGSTOP/SIGCONT, sentinel files, process group safety
  ltf/                  Loop Trace Format emitter + writer
  notify/               Desktop notifications (macOS + Linux)
  parser/               JSONL parsers for Claude Code, Codex, and Gemini CLI
  watcher/              fsnotify watcher, tailer (line + whole-file modes), debouncer
demo/                   Simulator, trial script, E2E test suite
```

## Adding a New Agent Source

LoopGuard supports Claude Code, Codex, and Gemini CLI natively. To add support for a new agent (e.g., Cursor, Aider):

### 1. Create a Parser

Add `internal/parser/<agent>.go` implementing the `Parser` interface:

```go
type Parser interface {
    Parse(line []byte) ([]*ParsedEvent, error)
}
```

Your parser converts one line of the agent's JSONL log into `ParsedEvent` structs. The key fields:
- `Model` — model string for cost calculation
- `Tokens` — input/output/cache token counts
- `ToolName`, `ToolInput` — for spin detection fingerprinting
- `IsError`, `ErrorMsg` — for error echo detection
- `FilesChanged` — for stall detection

Add tests in `internal/parser/<agent>_test.go` and a fuzz test in `internal/parser/fuzz_test.go`.

### 2. Create a Discoverer

Add `internal/discovery/<agent>.go` implementing the `Discoverer` interface:

```go
type Discoverer interface {
    BasePath() string                             // root dir for fsnotify
    Discover(maxAge time.Duration) []*Session      // scan for active sessions
    Agent() string                                 // agent name
}
```

### 3. Register in the Daemon

In `internal/daemon/daemon.go`:
- Add the discoverer to the `discoverers` slice (gated by `cfg.Sources`)
- Register the parser in the `parsers` map with the agent name as key

### 4. Add Pricing

Add model entries to `internal/analyzer/pricing.go` in `DefaultPricing()`.

### 5. Update Config

Add the agent to `SourcesConfig` in `internal/config/config.go`.

## Code Style

- Standard Go formatting (`gofmt`/`goimports`)
- No external test frameworks — stdlib `testing` only
- Errors returned, not panicked — the daemon must never crash
- All exported functions have doc comments
- Linters enforced: errcheck, govet, staticcheck, unused, ineffassign

## Pull Request Process

1. Fork the repo and create a branch from `main`
2. Make your changes with tests
3. Ensure all checks pass locally:
   ```bash
   go build ./...
   go vet ./...
   make test
   make lint
   ```
4. Open a PR against `main` with a clear description of what and why
5. CI runs automatically (Go 1.24 + 1.26 matrix, lint)
6. One approval required for merge

## Reporting Issues

Open a GitHub issue with:
- What you expected vs what happened
- LoopGuard version (`loopguard --version`)
- OS and Go version
- Relevant log output (run with `--verbose` to get debug logs)

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
