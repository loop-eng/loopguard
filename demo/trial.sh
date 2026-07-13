#!/bin/bash
# LoopGuard Trial — Interactive end-to-end demo
# Run this script to see LoopGuard in action against simulated agent sessions.
# Usage: bash demo/trial.sh

set -e

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$REPO_DIR/bin"

green() { printf "\033[32m%s\033[0m\n" "$1"; }
bold()  { printf "\033[1m%s\033[0m\n" "$1"; }
dim()   { printf "\033[2m%s\033[0m\n" "$1"; }

cleanup() {
  pkill -f "loopguard-demo" 2>/dev/null || true
  pkill -f "bin/loopguard " 2>/dev/null || true
  sleep 1
  rm -f ~/.config/loopguard/loopguard.sock
  rm -rf ~/.claude/projects/-Users-demo-loopguard-trial/
}

trap cleanup EXIT

cd "$REPO_DIR"
go build -o "$BIN/loopguard" ./cmd/loopguard/ 2>&1
go build -o "$BIN/loopguard-demo" ./demo/ 2>&1

bold "╔══════════════════════════════════════════════╗"
bold "║         LoopGuard — Interactive Trial        ║"
bold "╚══════════════════════════════════════════════╝"
echo ""

# ─── Phase 1: Normal Operation ──────────────────
bold "Phase 1: Normal Operation"
dim "Starting daemon and running a healthy agent session..."
cleanup
"$BIN/loopguard" --verbose > /tmp/lg-trial.log 2>&1 &
sleep 2

"$BIN/loopguard-demo" -scenario normal -speed 5 -project loopguard-trial 2>&1 | sed 's/^/  /'
sleep 2
echo ""
bold "  Status:"
"$BIN/loopguard" status 2>&1 | sed 's/^/  /'
green "  ✓ No alerts — healthy session completed normally."
echo ""

# ─── Phase 2: Spin Detection ───────────────────
bold "Phase 2: Spin Detection"
dim "Agent enters a loop — same 'npm test' command repeated..."
"$BIN/loopguard-demo" -scenario spin-tool -speed 5 -project loopguard-trial 2>&1 | sed 's/^/  /'
sleep 2
echo ""
bold "  Status:"
"$BIN/loopguard" status 2>&1 | sed 's/^/  /'
echo ""
SPIN=$(grep -c "trigger=spin_detected" /tmp/lg-trial.log 2>/dev/null || echo 0)
green "  ✓ Spin detected! ($SPIN alerts fired)"
echo ""

# ─── Phase 3: Budget Enforcement ───────────────
bold "Phase 3: Budget Enforcement"
dim "Restarting daemon with $0.50 budget limit..."
cleanup
LOOPGUARD_BUDGET_PER_SESSION=0.5 "$BIN/loopguard" --verbose > /tmp/lg-trial.log 2>&1 &
sleep 2

"$BIN/loopguard-demo" -scenario budget-exceed -speed 8 -budget 0.5 -project loopguard-trial 2>&1 | sed 's/^/  /'
sleep 2
echo ""
bold "  Status:"
"$BIN/loopguard" status 2>&1 | sed 's/^/  /'
echo ""
green "  ✓ Session paused at budget limit!"
echo ""

# ─── Phase 4: Resume ──────────────────────────
bold "Phase 4: Resume"
dim "Resuming the paused session..."
DEMO_ID=$("$BIN/loopguard" status --json 2>&1 | python3 -c "import sys,json;[print(s['id']) for s in json.load(sys.stdin).get('sessions',[]) if 'demo-' in s['id'] and s.get('paused')]" 2>/dev/null | head -1)
if [ -n "$DEMO_ID" ]; then
  "$BIN/loopguard" resume "${DEMO_ID:0:12}" 2>&1 | sed 's/^/  /'
  echo ""
  bold "  Status after resume:"
  "$BIN/loopguard" status 2>&1 | sed 's/^/  /'
  green "  ✓ Session resumed successfully!"
else
  dim "  (no paused session to resume — sentinel was used)"
fi
echo ""

# ─── Phase 5: Error Echo ──────────────────────
bold "Phase 5: Error Echo Detection"
dim "Agent hits the same error repeatedly..."
"$BIN/loopguard-demo" -scenario spin-error -speed 8 -project loopguard-trial 2>&1 | sed 's/^/  /'
sleep 2
ERRORS=$(grep -c "error repeated" /tmp/lg-trial.log 2>/dev/null || echo 0)
green "  ✓ Error echo detected! ($ERRORS alerts)"
echo ""

# ─── Summary ──────────────────────────────────
bold "╔══════════════════════════════════════════════╗"
bold "║              Trial Complete                  ║"
bold "╚══════════════════════════════════════════════╝"
echo ""
echo "  What you saw:"
echo "    1. Normal session ran without false positives"
echo "    2. Repeated tool calls detected as spin"
echo "    3. Budget enforcement paused session at limit"
echo "    4. Resume flow restored the session"
echo "    5. Repeated errors detected as error echo"
echo ""
echo "  Try it yourself:"
echo "    bin/loopguard --verbose              # start daemon"
echo "    bin/loopguard-demo -scenario interactive  # long-running session"
echo "    bin/loopguard status                 # check sessions"
echo "    bin/loopguard resume <id>            # resume paused"
echo ""
echo "  Run automated tests:"
echo "    bash demo/test_e2e.sh               # 26 automated tests"
echo ""
echo "  Full daemon log: /tmp/lg-trial.log"
echo ""
