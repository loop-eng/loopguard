# LoopGuard — Use Cases & User Stories

## Persona Definitions

### P1: Solo Developer ("Indie Hacker")
- Uses Claude Code daily for personal projects
- Pays with personal API key or Pro subscription
- Budget-conscious, no corporate safety net
- Runs loops overnight or during meetings

### P2: Team Lead ("Engineering Manager")
- 5-20 engineers using AI coding agents
- Responsible for team's AI spend
- Needs visibility and guardrails without blocking productivity
- Reports costs to leadership quarterly

### P3: Platform Engineer ("DevOps/SRE")
- Manages developer tooling for 50+ engineers
- Needs standardized guardrails across the organization
- Integrates with existing monitoring (Datadog, Grafana)
- Cares about fleet-wide health, not individual sessions

---

## Core Use Cases

### UC1: Budget Protection (All Personas)

**Story:** "As a developer, I want my agent loops to automatically pause when they exceed a cost threshold, so I never wake up to a surprise bill."

**Trigger:** Session cost exceeds `per_session_usd` config value (default: $20)

**Flow:**
1. Developer starts Claude Code with `/goal "make all tests pass"`
2. Agent iterates, fixing code and re-running tests
3. LoopGuard watches the session JSONL file
4. At $16 (80% of $20), LoopGuard sends a desktop notification: "Session approaching budget limit ($16/$20)"
5. At $20, LoopGuard sends SIGSTOP to the Claude Code process
6. Desktop notification: "Agent paused — budget limit reached ($20.12). Run `loopguard resume <id>` to continue."
7. Developer reviews progress, decides whether to continue or stop
8. If resume: `loopguard resume abc123` sends SIGCONT, agent continues from exact point

**Acceptance Criteria:**
- [ ] Warn notification at 80% of budget
- [ ] Hard pause at 100% of budget
- [ ] Agent state preserved (can resume without data loss)
- [ ] Cost displayed in notification
- [ ] `loopguard status` shows paused session with reason

---

### UC2: Spin Detection (P1, P2)

**Story:** "As a developer, I want LoopGuard to detect when my agent is stuck in a loop and pause it, so I don't waste tokens on unproductive work."

**Trigger:** Same tool call repeated 3+ times, or same error message 3+ times

**Flow:**
1. Agent encounters a test failure
2. Agent edits `src/auth.ts`, runs `npm test` — tests fail
3. Agent edits `src/auth.ts` (same file, similar change), runs `npm test` — tests fail again
4. Agent edits `src/auth.ts` again with a slightly different approach, runs `npm test` — same error
5. LoopGuard detects: `(tool=Bash, args_hash=abc123)` appeared 3 times in last 10 events
6. LoopGuard pauses the agent
7. Notification: "Spin detected — agent repeated the same action 3 times. Session paused."

**Acceptance Criteria:**
- [ ] Detect repeated tool calls (same tool + same argument hash)
- [ ] Detect repeated error messages (same error substring 3x)
- [ ] Don't false-positive on legitimate retries (different args = not a spin)
- [ ] Include which action was repeated in the notification

---

### UC3: No-Progress Stall Detection (P1, P2)

**Story:** "As a developer, I want to be alerted when my agent has been spending tokens for 10+ minutes without making any code changes."

**Trigger:** Ongoing token spend with no file modifications for `stall_minutes` (default: 10)

**Flow:**
1. Agent is running a research/discovery loop
2. Agent calls Read, Bash (grep), WebSearch repeatedly — gathering information
3. 10 minutes pass with continuous token spend but no Edit/Write tool calls
4. LoopGuard sends warning notification: "Agent may be stalled — spending tokens for 10 minutes without modifying files. Cost: $3.42"
5. Developer can check on the agent or let it continue

**Note:** This is a warning, not a hard pause — research/exploration loops legitimately read without writing.

---

### UC4: Cost Velocity Alert (P1, P2)

**Story:** "As a developer, I want to be alerted when my agent is burning tokens faster than expected, so I can investigate before costs spiral."

**Trigger:** Cost per minute exceeds `cost_velocity_per_min` (default: $2.00/min)

**Flow:**
1. Agent is using Opus model for a complex task
2. Each iteration sends large context windows (200K tokens)
3. LoopGuard calculates rolling 5-minute average: $2.50/min
4. Notification: "High cost velocity — agent spending $2.50/min (threshold: $2.00/min). Estimated $150/hour."

---

### UC5: Multi-Agent Dashboard (P2, P3)

**Story:** "As a team lead, I want to see all running agent sessions across my team in one view, with cost and status for each."

**Flow:**
1. Run `loopguard status`
2. Output:
   ```
   LoopGuard — Active Sessions
   
   ID       Agent    Project              Cost     Rate     Status    Duration
   abc123   claude   auth-service         $4.23    $0.12/m  running   35m
   def456   claude   frontend-app         $12.50   $0.08/m  running   2h15m
   ghi789   codex    data-pipeline        $1.80    $0.45/m  ⚠ spin    12m
   jkl012   claude   api-gateway          $20.00   —        paused    1h40m
   
   Today: $38.53 | Hour: $12.30 | Budget: $200/day (19%)
   ```

---

### UC6: Zero-Config First Run (P1)

**Story:** "As a new user, I want LoopGuard to work immediately after install with no configuration."

**Flow:**
1. `brew install loop-eng/tap/loopguard`
2. `loopguard`
3. LoopGuard auto-discovers Claude Code session directory
4. Starts watching with default settings:
   - $20/session budget
   - $50/hour across all sessions
   - $200/day total
   - Spin detection enabled
   - Desktop notifications enabled
5. Output: "LoopGuard running. Watching 2 active Claude Code sessions. No config file — using defaults."

---

### UC7: Overnight Loop Protection (P1)

**Story:** "As a developer, I want to start an agent loop before bed and trust that LoopGuard will prevent runaway costs while I sleep."

**Flow:**
1. Developer starts `claude --goal "refactor the auth module"` at 11 PM
2. Developer runs `loopguard install` (one-time) — sets up auto-start on login
3. LoopGuard daemon watches the session
4. At 2 AM, agent hits a spin (retrying same approach 5 times)
5. LoopGuard pauses the agent at $8.50
6. Desktop notification delivered (visible when developer opens laptop in morning)
7. LTF trace records the intervention with timestamp, reason, and cost

---

### UC8: Resume After Investigation (P1)

**Story:** "As a developer, I want to resume a paused agent after investigating why it was paused, with the option to adjust the budget."

**Flow:**
1. `loopguard status` — shows paused session with reason
2. Developer reads the LTF trace: `~/.config/loopguard/traces/abc123.ltf.jsonl`
3. Developer fixes the underlying issue (e.g., fixes a flaky test)
4. `loopguard resume abc123` — sends SIGCONT, agent continues
5. If budget was the issue: edit `~/.config/loopguard/config.yaml`, increase `per_session_usd: 50`
6. LoopGuard hot-reloads config (watches its own config file)

---

## Edge Cases & Error Scenarios

### EC1: Agent Process Dies While Paused
- LoopGuard detects process no longer exists
- Marks session as `terminated`
- Logs the event: "Session abc123 process exited while paused"

### EC2: Multiple LoopGuard Instances
- Second instance tries to connect to Unix socket
- Connection succeeds → "LoopGuard daemon already running"
- Exit with helpful message

### EC3: Permission Denied for SIGSTOP
- `syscall.Kill` returns `EPERM`
- Fall back to sentinel file: write `.loopguard-stop` in project directory
- Log warning: "SIGSTOP failed (permission denied), wrote sentinel file instead"

### EC4: Session Log File Deleted
- fsnotify fires `Remove` event
- Remove session from registry
- Clean up associated watcher
- No alarm — session probably completed normally

### EC5: Corrupt JSONL Line
- `json.Unmarshal` fails
- Skip line, log warning with line number
- Continue processing next line
- Don't crash the daemon over a single bad line

### EC6: Disk Full (Can't Write LTF)
- LTF writer fails
- Log error
- Continue enforcement (notifications + SIGSTOP still work)
- Don't let LTF failure block the circuit breaker

### EC7: Agent Using Unknown Model
- Model name not in pricing table
- Use Sonnet pricing as fallback (most common model)
- Log warning: "Unknown model 'xyz', using default pricing"
