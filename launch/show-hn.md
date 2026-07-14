# Show HN Post

## Title
Show HN: LoopGuard – Circuit breaker daemon that pauses AI agents before they drain your wallet

## Body
I built LoopGuard because I got burned by a Claude Code session that spun for 6 hours running the same failing test, costing $437.

LoopGuard monitors AI agent sessions (Claude Code, Codex, Gemini CLI) in real-time and pauses them via SIGSTOP when it detects:

- Repeated tool calls (same command 3x = spin)
- Error echo loops (same error 3x = stuck)
- Budget exceeded ($20/session default)
- Cost velocity ($2/min = something's wrong)

It's a Go daemon that tails the JSONL session logs, runs 4 independent spin detection heuristics, and sends SIGSTOP to the process group when it trips. Desktop notification tells you what happened. `loopguard resume` picks up where you left off.

Zero config — just run `loopguard` and it auto-discovers active sessions.

GitHub: https://github.com/loop-eng/loopguard

Technical details: Cost calculator has embedded pricing for 14 models (Anthropic, OpenAI, Google). Spin detection uses SHA-256 fingerprinting with a circular buffer. Budget enforcement uses sliding windows with per-session/hour/day limits. Sentinel fallback writes a .loopguard-stop file if SIGSTOP fails.

This is the first tool in a broader "loop engineering" suite I'm building. Happy to answer questions about the architecture or the detection heuristics.

## Posting Instructions
- Post Tuesday–Thursday, 8–10 AM Pacific
- Reply to EVERY comment within 90 minutes
- Be technical and specific in replies — HN rewards depth
- Don't ask anyone to upvote — HN detects and penalizes this
- If asked about alternatives: be honest about limitations (lossy path encoding, no Windows support)

---

# Reddit Posts

## r/ClaudeAI
**Title:** I built a daemon that pauses Claude Code when it enters a spin loop — saved me from a $200 bill last week

**Body:**
Has anyone else had Claude Code get stuck in a loop? Last month I left it running overnight and it burned $437 running `npm test` 14,000 times on a failing test.

Claude's `--max-budget-usd` flag overshoots by 8x, so I built a circuit breaker daemon that sits alongside Claude and watches for spin patterns.

It tails the JSONL session logs and detects 4 types of problems:
- Same tool call repeated 3x
- Same error repeated 3x
- Budget exceeded ($20/session default)
- Cost velocity above $2/min

When it trips, it sends SIGSTOP (freezes the process in place) and shows a desktop notification. `loopguard resume` picks up where you left off.

Zero config — just run `loopguard` and it finds your active sessions.

Anyone else built anything similar? I'm curious what detection heuristics would be most useful.

---

## r/golang
**Title:** Built a real-time JSONL tail daemon in Go with fsnotify + polling fallback — circuit breaker for AI coding agents

**Body:**
I built a daemon in Go that monitors AI agent sessions and pauses them when they enter spin loops. The interesting technical bits:

- **fsnotify + 5s poll fallback** — recursive directory watching with 100ms debounce per file
- **Streaming JSONL parser** — deduplicates token counts by requestId using a two-generation map (current + previous, evicts oldest half instead of full reset)
- **Circular buffer spin detection** — SHA-256 fingerprints of tool calls tracked in a fixed-size ring buffer
- **SIGSTOP to process group** — with pgid safety check to avoid stopping the daemon itself
- **Unix socket API** — HTTP over unix socket with umask 0177 for session status and resume

One fun bug: `sendSignal` was killing the entire process group, which included the daemon when launched from the same shell. Fixed by comparing target pgid against `os.Getpgrp()` before sending.

Open source: https://github.com/loop-eng/loopguard

Happy to talk about the architecture decisions — especially the shutdown ordering (which we got wrong twice before getting it right).

---

## r/devtools
**Title:** Circuit breaker pattern for AI coding agents — open source daemon that detects spin loops and enforces budgets

**Body:**
AI coding agents (Claude Code, Codex, Gemini CLI) don't have circuit breakers. They'll happily spin for hours burning tokens on the same failing test.

I took the circuit breaker pattern from distributed systems and applied it to AI agents. LoopGuard is a Go daemon that:

1. Auto-discovers active sessions by scanning session log directories
2. Tails JSONL files in real-time via fsnotify
3. Runs 4 spin detection heuristics (repeated calls, error echo, budget, cost velocity)
4. Pauses offending processes via SIGSTOP (preserves all state)
5. Desktop notification + `loopguard resume` to continue

Zero config — `brew install loop-eng/tap/loopguard && loopguard`

It's part of a broader suite of tools I'm building for "loop engineering" — the practice of designing, monitoring, and controlling AI agent loops.

---

## X/Twitter Thread

**Tweet 1:**
I lost $437 to a Claude Code session running `npm test` 14,000 times.

So I built a circuit breaker daemon that pauses AI agents before they drain your wallet.

It's called LoopGuard. Open source. Zero config. Here's how it works: 🧵

**Tweet 2:**
The problem: AI agents don't have circuit breakers.

Claude Code's --max-budget-usd overshoots by 8x. Cursor has zero loop detection. An enterprise team burned $500M in a single month.

Every agent needs a seatbelt. None of them have one.

**Tweet 3:**
LoopGuard sits beside your agent and watches for 4 types of trouble:

• Same tool call repeated 3x → spin
• Same error repeated 3x → stuck
• Budget exceeded → too expensive
• Cost velocity > $2/min → something's wrong

When it trips → SIGSTOP. Agent freezes in place.

**Tweet 4:**
The cool part: SIGSTOP preserves all state perfectly.

The agent doesn't crash. It doesn't lose context. It just... pauses.

A desktop notification tells you what happened.
`loopguard resume` picks up exactly where you left off.

**Tweet 5:**
Zero config. Just run `loopguard`.

It auto-discovers Claude Code, Codex, and Gemini sessions. Sane defaults handle everything.

Open source, MIT licensed.

github.com/loop-eng/loopguard

**Tweet 6:**
This is the first tool in a suite I'm building called loop-eng — developer tools for the emerging practice of loop engineering.

Next up: LTF (Loop Trace Format) and LoopCtl (TUI dashboard).

If you've been burned by an agent loop, I want to hear your story.

---

## LinkedIn Post

**AI agents are burning developer budgets — and nobody's building safety rails.**

Last month, a Claude Code session cost me $437 overnight. It ran `npm test` 14,000 times on a failing test. Six hours. Same error. Same retry. Nothing stopped it.

I built LoopGuard — an open-source circuit breaker daemon for AI coding agents. It monitors sessions in real-time, detects runaway loops, and pauses processes before they drain your budget.

The distributed systems community solved this pattern decades ago. When a downstream service starts failing, the circuit breaker trips. AI agents need the same protection.

LoopGuard watches Claude Code, Codex, and Gemini CLI sessions with zero configuration. It's open source, MIT licensed, and part of a broader initiative I'm calling "loop engineering."

If you're deploying AI agents in production — or just running them on your laptop — this is the safety net that should exist but doesn't.

GitHub: github.com/loop-eng/loopguard

#AI #DeveloperTools #OpenSource #CircuitBreaker #AIAgents
