# LoopGuard — Circuit Breaker for AI Agent Loops

## Project Summary

LoopGuard is a lightweight Go daemon that watches AI agent sessions in real-time and kills runaway loops before they burn budgets. Zero-config, auto-discovers sessions, desktop notifications, hard budget enforcement.

**One-liner:** "Stop runaway AI agent loops before they burn your budget."

## Origin & Motivation

The #1 developer pain point in loop engineering is runaway costs:

| Incident | Cost | Root Cause |
|----------|------|------------|
| Uber annual AI budget | Burned in 4 months | No per-engineer caps on Claude Code |
| Enterprise single-month | $500M on Claude | No usage limits deployed |
| LangChain 4-agent loop | $47K over 11 days | Infinite loop, nobody noticed |
| Overnight agent run | $437 | 14,000 redundant tool calls |
| Weekend production agent | $4,200 in 63 hours | Demo code ran unattended in prod |

No standalone circuit breaker daemon for agent loops exists. The closest tool (tokscale, ~4.1K stars) is purely retrospective cost tracking — it cannot intervene.

## Competitive Landscape (Verified July 2026)

| Tool | Stars | What It Does | Key Gap |
|------|-------|-------------|---------|
| **tokscale** | ~4,100 | Rust+TS token usage tracker, 30+ agents, TUI + web, leaderboard | **Read-only** — no intervention, no budget enforcement, no spin detection |
| **ccusage** | ~16,500 | CLI cost report for Claude Code | Retrospective only, single agent |
| **claude-devtools** | ~3,600 | Electron app for Claude session inspection | Not real-time, no enforcement, Claude-only |
| **Claude Code built-in** | — | `--max-turns`, `/cost`, session cost display | No hard budget kill, no spin detection, no cross-agent |
| **Codex CLI built-in** | — | Per-task cost limits, token tracking | Codex-only, no daemon-level protection |
| **Cursor built-in** | — | Monthly spending caps in settings | Cursor-only, account-level not session-level |

**LoopGuard's unique position:** The only tool that combines real-time monitoring + hard budget enforcement + spin detection + multi-agent support + zero-config + data stays local.

## Key Design Decisions (Resolved)

- [x] **File watching:** fsnotify + periodic polling fallback (fsnotify doesn't support recursive watching natively — walk directory tree and add watches)
- [x] **Spin detection:** Five heuristics (repeated tool calls, error echo, no-progress stall, context bloat, cost velocity)
- [x] **Budget enforcement:** SIGSTOP (pause without kill) preferred, with SIGTERM fallback. Resume via CLI command.
- [x] **Configuration:** YAML with sane zero-config defaults (works without config file)
- [x] **Desktop notifications:** gen2brain/beeep for cross-platform + native macOS osascript fallback
- [x] **IPC:** HTTP API over Unix domain socket for CLI→daemon communication
- [x] **Service management:** kardianos/service for cross-platform daemon install (launchd on macOS, systemd on Linux)
- [x] **Trace emission:** LTF v1.0 JSONL format for all interventions
- [x] **Single instance:** Unix socket existence check (try connect; if success, already running)

## Data Sources — Session Log Formats

### Claude Code JSONL (Primary Source)

**Path:** `~/.claude/projects/<url-encoded-project-path>/<session-uuid>.jsonl`

**Structure per line:**
```json
{
  "type": "assistant",
  "uuid": "msg-uuid",
  "parentUuid": "parent-msg-uuid",
  "sessionId": "session-uuid",
  "timestamp": "ISO-8601",
  "cwd": "/path/to/project",
  "gitBranch": "main",
  "version": "1.0.47",
  "message": {
    "role": "assistant",
    "content": [
      {"type": "text", "text": "..."},
      {"type": "tool_use", "id": "toolu_...", "name": "Bash", "input": {"command": "npm test"}},
      {"type": "tool_result", "tool_use_id": "toolu_...", "content": "..."}
    ],
    "model": "claude-sonnet-4-6",
    "usage": {
      "input_tokens": 12000,
      "output_tokens": 450,
      "cache_creation_input_tokens": 8000,
      "cache_read_input_tokens": 4000
    }
  }
}
```

**Parsing nuances:**
- Lines are streaming fragments, not complete messages. Multiple lines can represent one AI response.
- Same UUID may appear in multiple files during branching/resumption. Deduplicate by UUID.
- Token counts in partial streaming lines may be understated.
- Active sessions: check if the session process is still running (PID detection).

### Codex CLI JSONL

**Path:** `~/.codex/sessions/<session-id>/*.jsonl`

**Key events:** `codex_turn_started`, `codex_turn_ended`, `inference_completed` (tokens), `tool_call_started/ended`.

### Gemini CLI

**Path:** `$GEMINI_CLI_HOME/tmp/*/chats/*.json` (JSON, not JSONL)

## Model Pricing Table (July 2026)

Used for cost calculation from token counts:

### Anthropic Models
| Model | Input $/M | Output $/M | Cache Read $/M |
|-------|-----------|------------|----------------|
| claude-opus-4-8 | $5.00 | $25.00 | $0.50 |
| claude-opus-4-7 | $5.00 | $25.00 | $0.50 |
| claude-opus-4-6 | $5.00 | $25.00 | $0.50 |
| claude-sonnet-4-6 | $3.00 | $15.00 | $0.30 |
| claude-sonnet-4-5 | $3.00 | $15.00 | $0.30 |
| claude-haiku-4-5 | $1.00 | $5.00 | $0.10 |

### OpenAI Models
| Model | Input $/M | Output $/M |
|-------|-----------|------------|
| gpt-5.5 | $5.00 | $30.00 |
| gpt-4.1 | $2.00 | $8.00 |
| gpt-4.1-mini | $0.40 | $1.60 |
| o4-mini | $1.10 | $4.40 |
| o3 | $2.00 | $8.00 |

### Google Models
| Model | Input $/M | Output $/M |
|-------|-----------|------------|
| gemini-2.5-pro | $1.25 | $10.00 |
| gemini-2.5-flash | $0.15 | $0.60 |

## Technical Stack

| Component | Library | Version | Why |
|-----------|---------|---------|-----|
| Language | Go | 1.23+ | Fast, single binary, great for daemons |
| File watching | fsnotify | v1.10+ | De facto Go standard, 13.6k importers |
| JSONL parsing | encoding/json (stdlib) | — | No external dependency |
| Desktop notifications | gen2brain/beeep | latest | Cross-platform (macOS, Linux, Windows) |
| Config | spf13/viper | latest | YAML/TOML/env var support |
| CLI | spf13/cobra | latest | Standard Go CLI framework |
| Service management | kardianos/service | latest | Cross-platform daemon (launchd/systemd) |
| Logging | log/slog (Go 1.21+) | stdlib | Structured logging |
| IPC | net/http over Unix socket | stdlib | CLI→daemon communication |
| Binary releases | goreleaser | latest | Cross-platform binary builds |

## Related Files

- [Idea Document](/ideas/02-loopguard.md) — Original product concept
- [Architecture](/loopguard/context/ARCHITECTURE.md) — Technical architecture deep dive
- [Development Plan](/loopguard/context/DEVELOPMENT_PLAN.md) — Phase-wise build plan
- [Use Cases](/loopguard/context/USE_CASES.md) — User stories and scenarios
- [Research Report](/RESEARCH_REPORT.md) — Broader loop engineering landscape
