#!/bin/bash
# Compact demo for asciinema recording
# Usage: asciinema rec demo.cast -c "bash demo/record.sh"
set -e

REPO="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$REPO/bin"
cd "$REPO"

green() { printf "\033[32m%s\033[0m" "$1"; }
bold()  { printf "\033[1m%s\033[0m" "$1"; }
cyan()  { printf "\033[36m%s\033[0m" "$1"; }
type_slow() { echo ""; printf "\033[2m\$ %s\033[0m\n" "$1"; sleep 0.5; }

cleanup() {
  pkill -f "loopguard-demo" 2>/dev/null || true
  pkill -f "bin/loopguard " 2>/dev/null || true
  sleep 1
  rm -f ~/.config/loopguard/loopguard.sock
  rm -rf ~/.claude/projects/-Users-demo-loopguard-recording/
}
trap cleanup EXIT
cleanup

go build -o "$BIN/loopguard" ./cmd/loopguard/ 2>/dev/null
go build -o "$BIN/loopguard-demo" ./demo/ 2>/dev/null

bold "LoopGuard"; echo " — Circuit breaker for AI agent loops"
echo ""
sleep 1

# 1. Start daemon
type_slow "loopguard --verbose &"
LOOPGUARD_BUDGET_PER_SESSION=0.5 "$BIN/loopguard" --verbose > /tmp/lg-rec.log 2>&1 &
sleep 2
green "✓"; echo " Daemon started — watching Claude Code & Codex sessions"
sleep 1

# 2. Show normal operation
type_slow "loopguard-demo -scenario normal -speed 8"
"$BIN/loopguard-demo" -scenario normal -speed 8 -project loopguard-recording > /dev/null 2>&1
sleep 2
type_slow "loopguard status"
"$BIN/loopguard" status 2>&1 | grep -E "demo-|ID "
echo ""
green "✓"; echo " Normal session — no false positives"
sleep 1.5

# 3. Spin detection
type_slow "loopguard-demo -scenario spin-tool -speed 8"
"$BIN/loopguard-demo" -scenario spin-tool -speed 8 -project loopguard-recording > /dev/null 2>&1
sleep 2
type_slow "loopguard status"
"$BIN/loopguard" status 2>&1 | grep -E "demo-spin|ID "
echo ""
SPIN=$(grep -c "spin_detected" /tmp/lg-rec.log 2>/dev/null || echo 0)
green "✓"; echo " Spin detected! $SPIN alerts — session $(bold 'paused')"
sleep 1.5

# 4. Budget enforcement
type_slow "loopguard-demo -scenario budget-exceed -speed 10 -budget 0.5"
"$BIN/loopguard-demo" -scenario budget-exceed -speed 10 -budget 0.5 -project loopguard-recording > /dev/null 2>&1
sleep 2
type_slow "loopguard status"
"$BIN/loopguard" status 2>&1 | grep -E "demo-bud|ID "
echo ""
green "✓"; echo " Budget exceeded (\$0.50 limit) — session $(bold 'paused')"
sleep 1.5

# 5. Resume
DEMO_ID=$("$BIN/loopguard" status --json 2>&1 | python3 -c "import sys,json;[print(s['id']) for s in json.load(sys.stdin).get('sessions',[]) if 'demo-budget' in s['id'] and s.get('paused')]" 2>/dev/null | head -1)
if [ -n "$DEMO_ID" ]; then
  type_slow "loopguard resume ${DEMO_ID:0:8}"
  "$BIN/loopguard" resume "${DEMO_ID:0:12}" 2>&1
  sleep 1
  green "✓"; echo " Session resumed"
fi
sleep 1.5

echo ""
bold "Done."; echo " Zero config. Just run $(cyan 'loopguard')."
echo ""
echo "  GitHub:  github.com/loop-eng/loopguard"
echo "  Install: brew install loop-eng/tap/loopguard"
echo ""
sleep 3
