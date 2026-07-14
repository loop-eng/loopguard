# The $437 npm test Loop: Why AI Agents Need Circuit Breakers

*Publish on: dev.to, Medium (Let's Code Future), personal blog*
*Tags: ai, golang, devtools, opensource*

---

I left Claude Code running overnight on a refactor. Woke up to a $437 bill. The agent had run `npm test` 14,000 times — same failing test, same error, same retry. For six hours straight.

This wasn't a hallucination. It was a loop. And nothing stopped it.

## The Problem Is Bigger Than One Bad Night

I started researching and found I wasn't alone:

| Incident | Cost | What Happened |
|----------|------|---------------|
| Enterprise team, single month | $500M+ on Claude | No usage limits deployed |
| Overnight agent run | $437 | 14,000 redundant tool calls |
| LangChain 4-agent loop | $47K over 11 days | Infinite loop, nobody noticed |

Claude Code's `--max-budget-usd` flag [overshoots by 8x](https://github.com/anthropics/claude-code/issues/4277). Cursor has zero loop detection. Codex doesn't enforce cost limits. Every AI coding agent ships without a circuit breaker.

In distributed systems, we solved this decades ago. When a downstream service starts failing, the circuit breaker trips and stops the bleeding. The same pattern applies to AI agents — they're just processes making repeated calls that cost money.

## What I Built

[LoopGuard](https://github.com/loop-eng/loopguard) is a circuit breaker daemon for AI agent loops. It monitors Claude Code, Codex, and Gemini CLI sessions in real-time and pauses them when it detects trouble.

Four independent heuristics run continuously:

**1. Repeated Tool Calls** — If the agent calls the same tool with the same arguments 3 times in a row, it's spinning. LoopGuard fingerprints each call with SHA-256 and uses a circular buffer to track the last 50 calls.

**2. Error Echo** — If the agent hits the same error 3 times, it's stuck. Error messages are normalized and tracked in a separate circular buffer.

**3. Budget Enforcement** — Hard limits: $20/session, $50/hour, $200/day (all configurable). Warning at 80%, pause at 100%. Unlike Claude's built-in flag, this one actually stops the process.

**4. Cost Velocity** — If the burn rate exceeds $2/minute over a 5-minute sliding window, something is wrong. This catches expensive loops that use different tools each iteration.

When any heuristic trips, LoopGuard sends SIGSTOP to the process group. The agent freezes in place — all state preserved. A desktop notification tells you what happened. When you're ready, `loopguard resume` sends SIGCONT and the agent picks up exactly where it left off.

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

The daemon tails the JSONL session logs that Claude Code and Codex already write. No agent modification needed. No config file needed. Just run `loopguard` and it auto-discovers active sessions.

## Zero Config

```bash
# Install
brew install loop-eng/tap/loopguard

# Run (auto-discovers all active sessions)
loopguard

# Check what's being monitored
loopguard status

# Resume a paused session
loopguard resume <id>
```

That's it. Sane defaults handle everything. If you want to customize, `loopguard config init` creates a YAML file with full documentation.

## The Engineering Behind It

This started as a weekend project but turned into something I'm proud of:

- **40 bugs found and fixed** through 12 audit passes — 4 parallel review agents, adversarial testing, live SIGSTOP enforcement on real processes, fuzz testing with 45.7 million random inputs
- **94 tests** — 68 Go unit/integration tests + 26 end-to-end tests
- **Race detector clean** across all 7 packages
- **golangci-lint clean** — zero issues
- **Real-time cost tracking verified** against actual Claude Code sessions

The cost calculator has embedded pricing for 14 models across Anthropic, OpenAI, and Google — including cache read/write token pricing for Anthropic models.

## What's Next: Loop Engineering

LoopGuard is the first tool in a broader suite I'm calling [loop-eng](https://github.com/loop-eng) — developer tools for the emerging discipline of loop engineering:

| Tool | Purpose |
|------|---------|
| **LoopGuard** | Circuit breaker daemon (shipped) |
| **LTF** | Loop Trace Format specification |
| **LoopCtl** | TUI dashboard for session monitoring |
| **Kit** | Loop scaffolding CLI |
| **Loop-Bench** | Loop design benchmarking |
| **LoopReplay** | Session step-through debugger |

Every intervention LoopGuard makes is recorded as an [LTF](https://github.com/loop-eng/ltf) trace event — a standardized JSONL format that other tools can consume.

## Try It

```bash
# Install and run
brew install loop-eng/tap/loopguard
loopguard

# Or run the interactive demo
git clone https://github.com/loop-eng/loopguard
cd loopguard
bash demo/trial.sh
```

The demo walks through all 5 detection scenarios in 30 seconds, showing exactly how each heuristic fires and how enforcement works.

**GitHub:** [github.com/loop-eng/loopguard](https://github.com/loop-eng/loopguard)

---

*If you've been burned by an AI agent loop, I'd love to hear your story. And if you have ideas for detection heuristics I haven't thought of, open an issue or a discussion.*
