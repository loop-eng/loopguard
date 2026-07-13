# LoopGuard Demo & Test Harness

Real-time simulator for testing LoopGuard's detection and enforcement pipeline.

## One-Command Trial

```bash
bash demo/trial.sh
```

Walks through all 5 detection/enforcement features in sequence: normal operation, spin detection, budget enforcement, resume flow, and error echo. Takes ~30 seconds.

## Quick Start (Manual)

```bash
# Build
go build -o bin/loopguard ./cmd/loopguard/
go build -o bin/loopguard-demo ./demo/

# Terminal 1: Start daemon
bin/loopguard --verbose

# Terminal 2: Run a scenario
bin/loopguard-demo -scenario spin-tool
```

## Scenarios

| Scenario | What it does | Expected LoopGuard response |
|---|---|---|
| `normal` | Varied tool calls, file edits | No alerts (verifies no false positives) |
| `spin-tool` | 10x identical `npm test` | Pause after 3rd identical call |
| `spin-error` | 8x identical TypeError | Pause after 3rd identical error |
| `budget-exceed` | Escalating token usage | Warning at 80%, pause at 100% |
| `cost-velocity` | Large token bursts | Pause when $/min exceeds threshold |
| `stall` | One edit then only reads | Warning after 10min of no edits |
| `interactive` | Long-lived process, real PID | Manual testing — try `loopguard status` and `loopguard resume` |

## Interactive Mode

The `interactive` scenario is the most useful for manual testing. It:
- Runs as a long-lived process (so LoopGuard discovers its PID)
- Writes events every 5 seconds
- Prints its PID so you can see it in `loopguard status`
- Responds to SIGSTOP/SIGCONT (prints when paused/resumed)

```bash
# Terminal 1
bin/loopguard --verbose

# Terminal 2
bin/loopguard-demo -scenario interactive

# Terminal 3
loopguard status          # see the session with cost
loopguard resume <id>     # resume after LoopGuard pauses it
```

## Flags

| Flag | Default | Description |
|---|---|---|
| `-scenario` | `normal` | Which scenario to run |
| `-model` | `claude-opus-4-6[1m]` | Model string in JSONL events |
| `-speed` | `1.0` | Speed multiplier (5.0 = 5x faster) |
| `-budget` | `20.0` | Per-session budget for cost calibration |
| `-project` | `loopguard-test` | Project name for session directory |

## Automated E2E Test Suite

```bash
bash demo/test_e2e.sh
```

Runs 8 tests covering spin detection, error echo, budget enforcement, false positive prevention, concurrent sessions, env var overrides, graceful shutdown, and cost tracking. Exits 0 if all pass, 1 if any fail.

## Customizing Budget for Testing

Use env vars to lower the budget so budget scenarios trigger faster:

```bash
LOOPGUARD_BUDGET_PER_SESSION=5 bin/loopguard --verbose
bin/loopguard-demo -scenario budget-exceed -budget 5
```
