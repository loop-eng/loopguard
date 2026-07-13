# Engineering Handoff — Loop Engineering Projects

## How We Build

Every repo in the loop-eng org follows the same exhaustive, multi-pass engineering process. This document captures the methodology so any future session can replicate it exactly.

---

## Phase 0: Scaffold

- Set up Go module, directory structure, CI, linting, Makefile, goreleaser
- CI matrix: Go 1.24 + Go 1.26, golangci-lint v2 with action@v7
- Git config: local email `rajfirke23@gmail.com`, no AI attribution in commits or pushes — all commits under the user's GitHub account
- Push to the `loop-eng` GitHub org

## Phase 1-3: Implementation

- Build each phase iteratively: write code, review it, test it, fan out agents to check for bugs, fix them, test again
- Use `/god-mode` for maximum thoroughness — every phase gets the full treatment
- After each phase: build, vet, test, lint before moving on

## Phase 4: Polish & Distribution

- README with badges, competitive table, full config reference, architecture diagram
- `.goreleaser.yaml` for macOS/Linux arm64/amd64 + Homebrew tap
- Makefile with ldflags version injection
- `config init` with O_EXCL atomic create

---

## The Bug Hunt Protocol

This is where we differentiate. After implementation is complete, run an exhaustive multi-pass audit:

### Pass 1-2: Parallel Agent Audit
- Fan out 4 agents in parallel, each covering a different area of the codebase
- Agent 1: Core logic (analyzers, calculators, detectors)
- Agent 2: Parsers + file watchers
- Agent 3: Enforcement + discovery + notifications
- Agent 4: Daemon lifecycle + API + CLI + config
- Each agent reads ALL files in their scope and returns a JSON array of findings with: file, line, severity, description, evidence, fix
- Collect all findings, deduplicate, map to a FINDINGS.md file
- Verify each finding — confirm or dismiss with concrete evidence
- Fix all confirmed bugs

### Pass 3: Integration Path Audit
- Fan out an agent focused exclusively on cross-component interactions
- Test the full pipeline: discovery → watcher → parser → analyzer → enforcer
- Look for bugs INTRODUCED by the fixes themselves
- Check shutdown ordering, lock ordering, channel lifecycle
- Race conditions between concurrent goroutines

### Pass 4: Deferred Items
- Go back and fix every item that was deferred as "not critical"
- Nothing stays unfixed — every confirmed bug gets resolved

### Pass 5: Live System Testing
- Run the daemon against REAL sessions on the actual machine
- Verify cost tracking, session discovery, timestamp extraction against live data
- Test the resume flow end-to-end

### Pass 6: Adversarial Testing
- Malformed inputs: binary garbage, empty files, 2MB lines, partial JSON
- Config edge cases: negative values, zero values, huge values
- Rapid start/stop cycles (5x in quick succession)
- Concurrent session creation (3-5 simultaneous)
- No panics, no crashes, no data loss

### Pass 7: Live Enforcement
- Test actual SIGSTOP/SIGCONT on a real running process
- Verify the process state changes to T (stopped)
- Verify SIGCONT resumes the process
- Verify LTF trace files and history.jsonl are written correctly
- Test process group safety (daemon must not SIGSTOP itself)

### Pass 8: Untested Paths
- Every parser path (not just the primary one)
- Every CLI command with edge cases (no args, wrong args, daemon offline)
- Stale socket recovery
- Dual daemon prevention
- Any code path that hasn't been exercised yet

### Pass 9: Last Items
- Fix literally everything — even "plausible but unverified" items
- Read real session data to fix lossy encodings
- Fix platform-specific escaping issues

### Pass 10: golangci-lint
- Install and run golangci-lint locally (catches things go vet misses)
- staticcheck, unused, errcheck — fix all findings
- This often catches 2-3 real issues that static review missed

### Pass 11: Integration Tests
- Write Go integration tests that exercise the internal pipeline in-process
- No filesystem timing dependencies — deterministic inputs, deterministic outputs
- Cover every detection heuristic, every budget edge case, every parser path
- Test idempotent operations (double-close, double-stop)

### Pass 12: Fuzz Testing
- Write Go fuzz tests for every parser and every input-processing component
- Run for 30 seconds each — generates millions of random inputs
- Catches panics, infinite loops, and memory issues no human would find

### Pass 13: CI Verification
- Push and verify CI passes on all matrix targets
- This is the final gate — if CI is green, the code ships

---

## The Demo Protocol

Every repo gets a demo project that serves as both a test harness and a user-facing trial:

### Structure
```
demo/
├── main.go          # Simulator with multiple scenarios
├── trial.sh         # One-command interactive demo (shows all features)
├── test_e2e.sh      # Automated E2E test suite with pass/fail assertions
├── cleanup.sh       # Cleans up test artifacts
└── README.md        # Usage guide
```

### Requirements
1. **Multiple scenarios** — normal operation, every detection heuristic, budget enforcement, error cases
2. **Interactive mode** — long-lived process with real PID for manual testing
3. **Automated E2E suite** — bash script with green/red pass/fail output, covers every feature
4. **One-command trial** — `bash demo/trial.sh` walks through everything in 30 seconds
5. **Speed control** — `-speed` flag for fast testing without changing behavior
6. **Signal handling** — responds to SIGCONT so enforcement is visible

### E2E Test Coverage Targets
- Every detection heuristic (spin, error echo, budget, cost velocity)
- No false positives (normal operation must not trigger alerts)
- Resume flow (pause → resume → verify state)
- Config overrides (env vars honored)
- Concurrent sessions
- Graceful shutdown
- Cost tracking accuracy
- Stale state recovery
- Multi-parser paths
- CLI error handling

---

## Verification Checklist

Before declaring a repo done, every item must be green:

```
[ ] go build ./...                    — clean
[ ] go vet ./...                      — clean
[ ] golangci-lint run ./...           — 0 issues
[ ] go test -race -count=1 ./...     — all pass, 0 races
[ ] go test -fuzz (30s per target)   — 0 panics
[ ] govulncheck ./...                — 0 code vulnerabilities
[ ] go mod tidy                      — no changes
[ ] make build / make test / make lint — all work
[ ] bash demo/test_e2e.sh            — all pass
[ ] bash demo/trial.sh               — runs successfully
[ ] gh run list --limit 1            — CI success
[ ] gh issue list --state open       — 0 open issues
[ ] FINDINGS.md                      — 0 NOT FIXED entries
[ ] README.md                        — accurate after all changes
```

---

## Principles

1. **Fix everything.** No "deferred" items, no "known issues," no "good enough." If it's confirmed, fix it.
2. **Verify with real data.** Don't just read code — run it against actual sessions, real processes, live signals.
3. **Adversarial testing.** Try to break it. Binary garbage, negative configs, rapid cycling, concurrent access.
4. **Fuzz what you parse.** Every input-processing function gets fuzz tests. Millions of random inputs.
5. **No false confidence.** Unit tests verify code correctness, not feature correctness. E2E tests verify the feature works. Both are required.
6. **Parallel agents for breadth.** Fan out 4 agents for initial audit — they catch different things because they have different context windows.
7. **Integration tests for depth.** In-process tests with deterministic inputs catch edge cases E2E tests can't reach.
8. **CI is the final gate.** If CI isn't green, it doesn't ship.
9. **The demo IS the test.** If the demo can't demonstrate a feature, the feature doesn't work.
10. **Document the hunt.** FINDINGS.md records every bug found, its severity, evidence, and fix. It's the audit trail.

---

## Numbers From the First Repo

For calibration on what "exhaustive" means:

- 12 audit passes
- 4 parallel review agents
- 40 bugs found and fixed (4 critical, 8 high, 21 medium, 7 low)
- 25 source files modified
- 68 Go unit/integration tests
- 26 E2E tests
- 3 fuzz targets, 45.7 million random inputs
- 0 panics, 0 races, 0 lint issues
- Live SIGSTOP/SIGCONT verified on real process
- Real session cost tracking verified against live data
