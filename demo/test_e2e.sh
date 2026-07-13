#!/bin/bash
set -e

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$REPO_DIR/bin"
LOG_FILE="/tmp/loopguard-e2e-test.log"
PASS=0
FAIL=0

green() { printf "\033[32m%s\033[0m\n" "$1"; }
red()   { printf "\033[31m%s\033[0m\n" "$1"; }
bold()  { printf "\033[1m%s\033[0m\n" "$1"; }

assert_log() {
  if grep -qE "$1" "$LOG_FILE"; then
    green "  PASS: $2"
    PASS=$((PASS + 1))
  else
    red "  FAIL: $2"
    FAIL=$((FAIL + 1))
  fi
}

assert_status_contains() {
  local output
  output=$("$BIN/loopguard" status 2>&1) || true
  if echo "$output" | grep -q "$1"; then
    green "  PASS: $2"
    PASS=$((PASS + 1))
  else
    red "  FAIL: $2"
    FAIL=$((FAIL + 1))
  fi
}

full_cleanup() {
  pkill -f "loopguard-demo" 2>/dev/null || true
  pkill -f "bin/loopguard " 2>/dev/null || true
  sleep 1
  rm -f ~/.config/loopguard/loopguard.sock
  rm -rf ~/.claude/projects/-Users-demo-loopguard-test/
  rm -rf ~/.codex/sessions/e2e-codex-test/
}

start_daemon_with() {
  rm -f "$LOG_FILE"
  if [ -n "$1" ]; then
    env $1 "$BIN/loopguard" --verbose > "$LOG_FILE" 2>&1 &
  else
    "$BIN/loopguard" --verbose > "$LOG_FILE" 2>&1 &
  fi
  DAEMON_PID=$!
  # Wait for socket to appear
  local tries=0
  while [ $tries -lt 20 ]; do
    if [ -S "$HOME/.config/loopguard/loopguard.sock" ]; then
      sleep 0.5
      return 0
    fi
    sleep 0.5
    tries=$((tries + 1))
  done
  red "  Daemon did not start in time"
  return 1
}

stop_daemon() {
  if [ -n "$DAEMON_PID" ]; then
    kill "$DAEMON_PID" 2>/dev/null || true
    wait "$DAEMON_PID" 2>/dev/null || true
    DAEMON_PID=""
  fi
  rm -f ~/.config/loopguard/loopguard.sock
}

# ═══════════════════════════════════════════════════
bold "=== LoopGuard E2E Test Suite ==="
echo ""

bold "[Build]"
cd "$REPO_DIR"
go build -o "$BIN/loopguard" ./cmd/loopguard/
go build -o "$BIN/loopguard-demo" ./demo/
go test ./... 2>&1 | grep -E "^(ok|FAIL)" | tail -5
green "  Build OK"
echo ""

# ─── Test 1: Spin Detection ─────────────────────
bold "[Test 1] Spin Detection (repeated tool calls)"
full_cleanup
start_daemon_with ""
"$BIN/loopguard-demo" -scenario spin-tool -speed 5 > /dev/null 2>&1
sleep 3
assert_log "auto-registered session from watcher" "Session auto-registered"
assert_log "spin_detected.*repeated.*3 times" "Spin detected at call 3"
assert_log "level=pause.*trigger=spin_detected" "Pause alert emitted"
assert_status_contains "paused" "Session shows as paused"
stop_daemon
echo ""

# ─── Test 2: Error Echo ─────────────────────────
bold "[Test 2] Error Echo Detection"
full_cleanup
start_daemon_with ""
"$BIN/loopguard-demo" -scenario spin-error -speed 5 > /dev/null 2>&1
sleep 3
assert_log "spin_detected.*error repeated.*3 times" "Error echo detected at error 3"
assert_status_contains "paused" "Session shows as paused"
stop_daemon
echo ""

# ─── Test 3: Budget Warning + Exceeded ──────────
bold "[Test 3] Budget Warning + Exceeded (\$5 limit)"
full_cleanup
start_daemon_with "LOOPGUARD_BUDGET_PER_SESSION=5"
"$BIN/loopguard-demo" -scenario budget-exceed -speed 5 -budget 5 > /dev/null 2>&1
sleep 3
assert_log "budget_warning" "Budget warning emitted"
assert_log "budget_exceeded" "Budget exceeded emitted"
assert_log "sentinel fallback" "Sentinel fallback used"
assert_status_contains "paused" "Session shows as paused"
stop_daemon
echo ""

# ─── Test 4: Normal Session (no false positives) ─
bold "[Test 4] Normal Session (no false positives)"
full_cleanup
start_daemon_with ""
"$BIN/loopguard-demo" -scenario normal -speed 5 > /dev/null 2>&1
sleep 3
if grep -qE "trigger=spin_detected|trigger=budget_exceeded" "$LOG_FILE"; then
  red "  FAIL: False positive alert on normal session"
  FAIL=$((FAIL + 1))
else
  green "  PASS: No false positive alerts"
  PASS=$((PASS + 1))
fi
stop_daemon
echo ""

# ─── Test 5: Env Var Override ───────────────────
bold "[Test 5] Config Env Var Override"
full_cleanup
start_daemon_with "LOOPGUARD_BUDGET_PER_SESSION=1"
sleep 1
assert_log "budget_per_session=1" "Env override applied (per_session=1)"
stop_daemon
echo ""

# ─── Test 6: Concurrent Sessions ────────────────
bold "[Test 6] Multiple Concurrent Sessions"
full_cleanup
start_daemon_with ""
"$BIN/loopguard-demo" -scenario spin-tool -speed 10 > /dev/null 2>&1 &
PID1=$!
"$BIN/loopguard-demo" -scenario normal -speed 10 > /dev/null 2>&1 &
PID2=$!
wait $PID1 2>/dev/null || true
wait $PID2 2>/dev/null || true
sleep 3
assert_log "trigger=spin_detected" "Spin session triggered alert"
# Count distinct sessions
SESSIONS=$("$BIN/loopguard" status 2>&1 | grep -c "demo-" || echo 0)
if [ "$SESSIONS" -ge 2 ]; then
  green "  PASS: Both sessions tracked ($SESSIONS demo sessions)"
  PASS=$((PASS + 1))
else
  red "  FAIL: Expected 2+ demo sessions, got $SESSIONS"
  FAIL=$((FAIL + 1))
fi
stop_daemon
echo ""

# ─── Test 7: Graceful Shutdown ──────────────────
bold "[Test 7] Daemon Graceful Shutdown"
full_cleanup
start_daemon_with ""
sleep 1
kill "$DAEMON_PID" 2>/dev/null
wait "$DAEMON_PID" 2>/dev/null || true
DAEMON_PID=""
assert_log "daemon stopped" "Clean shutdown logged"
echo ""

# ─── Test 8: Cost Tracking ──────────────────────
bold "[Test 8] Cost Tracking Accuracy"
full_cleanup
start_daemon_with ""
"$BIN/loopguard-demo" -scenario normal -speed 10 > /dev/null 2>&1
sleep 3
STATUS_JSON=$("$BIN/loopguard" status --json 2>&1) || true
if echo "$STATUS_JSON" | python3 -c "import sys,json; d=json.load(sys.stdin); costs=[s['cost'] for s in d.get('sessions',[]) if 'demo-' in s['id']]; assert any(c>0 for c in costs)" 2>/dev/null; then
  green "  PASS: Non-zero cost tracked for demo session"
  PASS=$((PASS + 1))
else
  red "  FAIL: Cost is zero or missing for demo session"
  FAIL=$((FAIL + 1))
fi
stop_daemon
echo ""

# ─── Test 9: Resume Flow ─────────────────────────
bold "[Test 9] Resume Sentinel-Paused Session"
full_cleanup
start_daemon_with "LOOPGUARD_BUDGET_PER_SESSION=0.5"
"$BIN/loopguard-demo" -scenario budget-exceed -speed 10 -budget 0.5 > /dev/null 2>&1
sleep 3
assert_status_contains "paused" "Session paused after budget exceed"
RESUME_ID=$("$BIN/loopguard" status --json 2>&1 | python3 -c "import sys,json;[print(s['id']) for s in json.load(sys.stdin).get('sessions',[]) if 'demo-' in s['id'] and s.get('paused')]" 2>/dev/null | head -1)
if [ -n "$RESUME_ID" ]; then
  RESULT=$("$BIN/loopguard" resume "${RESUME_ID:0:12}" 2>&1)
  if echo "$RESULT" | grep -q "resumed"; then
    green "  PASS: Resume succeeded"
    PASS=$((PASS + 1))
  else
    red "  FAIL: Resume failed: $RESULT"
    FAIL=$((FAIL + 1))
  fi
  AFTER_STATE=$("$BIN/loopguard" status --json 2>&1 | python3 -c "import sys,json;[print(s['paused']) for s in json.load(sys.stdin).get('sessions',[]) if s['id']=='$RESUME_ID']" 2>/dev/null)
  if [ "$AFTER_STATE" = "False" ]; then
    green "  PASS: Session no longer paused after resume"
    PASS=$((PASS + 1))
  else
    red "  FAIL: Session still paused after resume"
    FAIL=$((FAIL + 1))
  fi
else
  red "  FAIL: No paused demo session found to resume"
  FAIL=$((FAIL + 2))
fi
stop_daemon
echo ""

# ─── Test 10: Stale Socket Recovery ──────────────
bold "[Test 10] Stale Socket Recovery"
full_cleanup
mkdir -p ~/.config/loopguard
touch ~/.config/loopguard/loopguard.sock
start_daemon_with ""
RESULT=$("$BIN/loopguard" status 2>&1)
if echo "$RESULT" | grep -q "Active Sessions"; then
  green "  PASS: Daemon started despite stale socket"
  PASS=$((PASS + 1))
else
  red "  FAIL: Daemon failed to start with stale socket"
  FAIL=$((FAIL + 1))
fi
stop_daemon
echo ""

# ─── Test 11: Dual Daemon Prevention ────────────
bold "[Test 11] Dual Daemon Prevention"
full_cleanup
start_daemon_with ""
SECOND=$("$BIN/loopguard" 2>&1)
if echo "$SECOND" | grep -q "already running"; then
  green "  PASS: Second daemon rejected"
  PASS=$((PASS + 1))
else
  red "  FAIL: Second daemon was not rejected"
  FAIL=$((FAIL + 1))
fi
stop_daemon
echo ""

# ─── Test 12: Codex Parser Path ─────────────────
bold "[Test 12] Codex Parser Path"
full_cleanup
mkdir -p ~/.codex/sessions/e2e-codex-test
CODEX_FILE=~/.codex/sessions/e2e-codex-test/events.jsonl
start_daemon_with ""
# Write Codex-format events with repeated tool calls
for i in $(seq 1 5); do
  echo "{\"type\":\"tool_call_started\",\"id\":\"tc$i\",\"session_id\":\"e2e-codex\",\"timestamp\":\"2026-07-12T10:00:0${i}Z\",\"data\":{\"name\":\"shell\",\"input\":\"npm test\"}}" >> "$CODEX_FILE"
  echo "{\"type\":\"inference_completed\",\"id\":\"inf$i\",\"session_id\":\"e2e-codex\",\"timestamp\":\"2026-07-12T10:00:0${i}Z\",\"data\":{\"model\":\"gpt-4.1\",\"input_tokens\":5000,\"output_tokens\":500,\"reasoning_output_tokens\":100,\"token_count\":5600}}" >> "$CODEX_FILE"
  sleep 0.3
done
sleep 3
assert_log "codex discovery complete" "Codex discoverer ran"
assert_status_contains "codex" "Codex session discovered"
assert_log "spin_detected" "Codex spin detected"
stop_daemon
rm -rf ~/.codex/sessions/e2e-codex-test
echo ""

# ─── Test 13: CLI Error Handling ────────────────
bold "[Test 13] CLI Error Handling"
stop_daemon
sleep 1
EC=0; "$BIN/loopguard" resume 2>/dev/null || EC=$?
if [ $EC -ne 0 ]; then
  green "  PASS: resume with no args returns error"
  PASS=$((PASS + 1))
else
  red "  FAIL: resume with no args should fail"
  FAIL=$((FAIL + 1))
fi
EC=0; "$BIN/loopguard" resume nonexistent 2>/dev/null || EC=$?
if [ $EC -ne 0 ]; then
  green "  PASS: resume when daemon offline returns error"
  PASS=$((PASS + 1))
else
  red "  FAIL: resume should fail when daemon offline"
  FAIL=$((FAIL + 1))
fi
echo ""

# ═══════════════════════════════════════════════════
full_cleanup
echo ""
bold "=== Results ==="
TOTAL=$((PASS + FAIL))
echo "  $PASS/$TOTAL passed"
if [ $FAIL -gt 0 ]; then
  red "  $FAIL FAILED"
  echo "  Full log: $LOG_FILE"
  exit 1
else
  green "  All tests passed!"
  exit 0
fi
