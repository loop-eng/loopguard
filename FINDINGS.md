# LoopGuard Bug Audit Findings

Generated: 2026-07-08
Method: 4 parallel audit agents + manual code review + E2E testing of all 52 Go source files

## Summary

| Severity | Count | Confirmed | Fixed |
|----------|-------|-----------|-------|
| Critical | 4     | 4         | 4     |
| High     | 8     | 8         | 8     |
| Medium   | 21    | 21        | 21    |
| Low      | 7     | 7         | 7     |
| **Total**| **40**| **40**    | **40**|

## E2E Verification

All fixes verified via live demo scenarios:
- **Spin detection (repeated tools):** Fires at 3rd identical call - PASS
- **Spin detection (error echo):** Fires at 3rd identical error - PASS
- **Budget warning (80%):** Fires at $4.38/$5.00 (88%) - PASS
- **Budget exceeded (100%):** Fires at $5.25/$5.00 (105%) with sentinel fallback - PASS
- **Cost tracking:** $13.12 accurately computed across 15 calls - PASS
- **Desktop notifications:** Sent for warnings and pauses - PASS
- **Session auto-registration:** New sessions picked up within 100ms - PASS
- **Env var overrides:** LOOPGUARD_BUDGET_PER_SESSION=5 honored - PASS
- **No false positives:** Normal sessions run without triggering alerts - PASS
- **Concurrent sessions:** Two sessions tracked independently - PASS
- **Graceful shutdown:** Clean shutdown with "daemon stopped" logged - PASS
- **Cost tracking accuracy:** Non-zero cost computed for sessions - PASS

**E2E test suite: 16/16 passed** (run via `bash demo/test_e2e.sh`)
- **Race detector:** No races detected (`go test -race`)

### Pass 3 Fixes (integration audit)
- **C3 (Critical):** Auto-registered sessions wrote sentinel to wrong directory (decoded path vs storage path), causing enforcement to silently fail while reporting success. Fixed with `inferSessionInfo()` that derives agent type from discoverer basePath and validates decoded project dir exists before using it.
- **M12 (Medium):** `applyDefaults` prevented YAML from disabling budget limits (zero → default). Fixed to only apply defaults for non-budget structural fields; budget `0` now means "disabled" consistently between YAML and env vars.
- **M7 (Medium):** Added `RemoveSession()` to both Analyzer and BudgetEnforcer for session cleanup.
- **M10 (Medium):** Resume API returns HTTP 404 for "session not found" (was 400).
- **M18 (Medium):** Codex sessions now set ProjectDir from session path.
- **L1 (Low):** Resume detects ambiguous prefix matches and asks for a longer prefix.
- **L3 (Low):** HTTP server has read/write/idle timeouts (10s/10s/60s).
- **L4 (Low):** Fixed variable shadowing in status.go.
- **L6 (Low):** `contentToString(nil)` returns `""` instead of `"null"`.
- **L7 (Low):** Tailer handles oversized partial lines with `skipToNewline` flag to prevent garbled data.

### Pass 4 Fixes (final deferred items)
- **M14 (Medium):** Kill follow-up now sends SIGKILL to entire process group (`-pgid`) to clean up orphaned children.
- **M16 (Medium):** Custom sessions check liveness via `lsof` instead of hardcoding `Active: true`.
- **M17 (Medium):** Each custom path gets its own discoverer instance so all directories get fsnotify watches.
- **L2 (Low):** History file opened with `O_NOFOLLOW` to eliminate TOCTOU symlink race window.
- **L5 (Low):** `StartedAt` read from first JSONL line timestamp instead of file ModTime.

### Verification
- **Unit tests:** All passing
- **Race detector:** Clean (`go test -race ./...`)
- **E2E tests:** 16/16 passing
- **All 40 bugs fixed — 0 remaining.**

### Pass 5 Fixes (live system testing)
- **H6 (High):** Resume fails for sentinel-paused sessions (PID=0). `enforcer.Resume()` required a valid PID for SIGCONT, making sentinel-paused sessions permanently stuck. Fixed: Resume now works without PID — removes sentinel and marks session active.
- **E2E test suite expanded:** 19 tests (was 16). Added Test 9: resume flow for sentinel-paused sessions.
- **Verified against real Claude Code sessions:** Cost tracking ($0.09 real session), session discovery, StartedAt from JSONL timestamps all confirmed working on live sessions.

### Pass 7 Fixes (live enforcement testing)
- **H7 (High):** `sendSignal` sends SIGSTOP to entire process group (`-pgid`), which freezes the daemon itself when daemon and agent share a process group (same shell session). Fixed: compare target pgid against `os.Getpgrp()` and fall back to single-PID signal when they match.
- **Live SIGSTOP verified:** Real process stopped (state T), SIGCONT resumed it, budget re-triggered immediately (correct behavior — budget limit still in effect).
- **LTF traces verified:** Proper JSONL with ltf_version, loop_id, agent, action, cost_usd, result, metadata.
- **History verified:** 42 entries, proper JSONL format with timestamps.
- **Adversarial testing verified:** Malformed JSONL (binary garbage, empty files, 2MB lines, partial JSON), negative/zero/huge config values, 5 rapid start/stop cycles — no panics, no crashes.

### Pass 8 (untested paths)
- **Codex parser end-to-end:** Codex session discovered, parsed with CodexParser, cost tracked ($0.07 via gpt-4.1 pricing), spin detection triggered — all verified.
- **CLI edge cases:** status offline, resume no-args, resume offline, version, help, config init, config init duplicate, unknown subcommand, stop without service — all return correct errors/output.
- **Stale socket recovery:** Daemon starts despite stale socket file.
- **Dual daemon prevention:** Second daemon launch rejected with "already running".
- **E2E test suite expanded:** 26 tests (was 19). Added stale socket, dual daemon, Codex parser, CLI error handling.

### Final verification (all passes combined)
- **Unit tests:** all pass
- **Race detector:** clean (`go test -race ./...`)
- **Go vet:** clean
- **E2E tests:** **26/26 passing**
- **Adversarial testing:** no crashes on malformed inputs
- **Live enforcement:** Real SIGSTOP/SIGCONT verified on live process
- **LTF traces + history:** Proper JSONL output verified
- **Real Claude Code sessions:** Cost tracking confirmed on live sessions

---

## Critical

### C0: Watcher isRelevant filter blocks directory Create events (found in E2E)
- **File:** `internal/watcher/watcher.go:72-85`
- **Status:** CONFIRMED | FIXED
- **Description:** The `isRelevant()` check was called BEFORE the directory Create handler. Since directory names don't end in `.jsonl`, isRelevant returned false, skipping the directory add. New directories created after daemon startup were never watched by fsnotify.
- **Impact:** Sessions created in new project directories after daemon start were invisible until the 5s polling fallback kicked in — or never if no tailer existed for the file.
- **Fix:** Moved directory Create check before the isRelevant filter.

### C1: Foreground daemon never calls Shutdown()
- **File:** `internal/cli/root.go:71`
- **Status:** CONFIRMED | FIXED
- **Description:** `runDaemon()` calls `d.Run()` and returns, but never calls `d.Shutdown()`. Goroutines leak, LTF file handles leak, watcher is never closed.
- **Impact:** Resource leak on every foreground run. Buffered LTF data lost.
- **Fix:** Added `defer d.Shutdown()` after daemon creation.

### C2: Shutdown order race — analyzer.Stop() before context cancel
- **File:** `internal/daemon/daemon.go:142`
- **Status:** CONFIRMED | FIXED
- **Description:** `Shutdown()` calls `analyzer.Stop()` (closes done channel) before `d.cancel()`. During this window, `processEvents()` can still call `analyzer.Process()` → `emit()`, which sees `done` closed and drops alerts silently.
- **Impact:** Alerts lost during shutdown window.
- **Fix:** Reordered: `cancel()` first, then `wg.Wait()`, then `analyzer.Stop()`, then cleanup.

---

## High

### H0: New sessions not registered until 60s rediscovery cycle (found in E2E)
- **File:** `internal/daemon/daemon.go:229-260`
- **Status:** CONFIRMED | FIXED
- **Description:** Sessions created after initial discovery were not added to the registry until the 60s rediscovery loop ran. The watcher processed their events and the analyzer detected issues, but `executeAlert()` couldn't find them in the registry, so all enforcement was skipped.
- **Impact:** New sessions were completely unprotected for up to 60 seconds.
- **Fix:** Auto-register sessions in `processEvents()` when the watcher emits events for an unknown session ID.

### H1: Budget check returns first violation, masking exceeded limits behind warnings
- **File:** `internal/analyzer/budget.go:72-90`
- **Status:** CONFIRMED | FIXED
- **Description:** `RecordCost()` returns the first non-nil `BudgetResult`. If per-session is at 85% (warning) but per-hour is at 110% (exceeded), only the warning is returned. The exceeded hourly budget is silently ignored.
- **Impact:** Session keeps spending past hard limits because the exceeded status is never surfaced.
- **Fix:** Collect all results, return the most severe (exceeded > warning).

### H2: Non-deterministic prefix match in cost calculator
- **File:** `internal/analyzer/cost.go:39`
- **Status:** CONFIRMED | FIXED
- **Description:** `resolve()` iterates a Go map for prefix matching. Map iteration order is random, so "gpt-4.1-mini-2025" could match either "gpt-4.1" ($2/$8) or "gpt-4.1-mini" ($0.40/$1.60) — a 5x pricing error.
- **Impact:** Silently wrong cost calculations, budget thresholds crossed at wrong times.
- **Fix:** Sort keys by length descending, match longest prefix first.

### H3: PID reuse TOCTOU in kill() follow-up goroutine
- **File:** `internal/enforcer/enforcer.go:108-118`
- **Status:** CONFIRMED | FIXED
- **Description:** After SIGTERM, a goroutine waits 5s then calls `validatePID()` and `killProcess()` separately. Between validation and kill, the PID could be recycled to an innocent process.
- **Impact:** Could SIGKILL an unrelated process (low probability but catastrophic).
- **Fix:** Capture pgid before spawning goroutine; verify process start time before SIGKILL.

### H4: Custom discoverer security check bypassed when HOME is unset
- **File:** `internal/discovery/custom.go:39-46`
- **Status:** CONFIRMED | FIXED
- **Description:** When `os.UserHomeDir()` fails, `home` is empty. The check `home != "" && !HasPrefix(...)` short-circuits to false, allowing any path.
- **Impact:** In containers/cron/systemd, the home-dir restriction is silently disabled.
- **Fix:** Reject all custom paths when home dir is unavailable.

### H5: Lossy decodeProjectDir corrupts paths with hyphens
- **File:** `internal/discovery/claude.go:85-87`
- **Status:** CONFIRMED | FIXED
- **Description:** All hyphens become path separators. "/my-project" → "/my/project". Documented as lossy but impacts sentinel file placement.
- **Fix:** `readSessionMeta()` extracts the `cwd` field from the JSONL file (first 20 lines). When present, it overrides the lossy decoded path. Verified on real sessions: `/Users/rfirke/Downloads/AI Projects/Loop Engineering` now shows correctly.

---

## Medium

### M1: Codex parser silently drops Data unmarshal errors
- **File:** `internal/parser/codex.go:56,70,87`
- **Status:** CONFIRMED | FIXED
- **Description:** `json.Unmarshal(entry.Data, &tc)` errors are discarded. Malformed events are silently swallowed.
- **Fix:** Return errors from unmarshal failures.

### M2: Sliding-window slices leak memory
- **File:** `internal/analyzer/budget.go:67`, `internal/analyzer/spin.go:215`
- **Status:** CONFIRMED | FIXED
- **Description:** Re-slicing `be.hourlyCosts = be.hourlyCosts[trimIdx:]` doesn't release the backing array.
- **Fix:** Copy to new slice when trimIdx > 0.

### M3: seenRequests reset causes token double-counting
- **File:** `internal/parser/claude.go:85-86`
- **Status:** CONFIRMED | FIXED
- **Description:** Full map reset means previously-seen requestIds are forgotten, causing tokens to be counted twice.
- **Fix:** Evict oldest half of entries instead of full reset.

### M4: readAndEmit TOCTOU race on tailer creation
- **File:** `internal/watcher/watcher.go:167-175`
- **Status:** CONFIRMED | FIXED
- **Description:** Check-then-create for tailers is not atomic. Two debouncer callbacks could create duplicate tailers.
- **Fix:** Hold the lock across the check-and-create.

### M5: AlertKill ignores enforcer.Execute error
- **File:** `internal/daemon/daemon.go:317`
- **Status:** CONFIRMED | FIXED
- **Description:** If `enforcer.Execute(ActionKill, ...)` fails, the session is still marked inactive and notifications sent as if it succeeded.
- **Fix:** Check error, log it, and only mark inactive if kill succeeded.

### M6: Analyzer.Stop() can panic on double close
- **File:** `internal/analyzer/analyzer.go:165`
- **Status:** CONFIRMED | FIXED
- **Description:** `close(a.done)` on an already-closed channel panics. No sync.Once guard.
- **Fix:** Added sync.Once to guard the close.

### M7: Unbounded growth of sessions and warned maps in Analyzer
- **File:** `internal/analyzer/analyzer.go:49,52`
- **Status:** CONFIRMED | FIXED
- **Description:** No mechanism to remove entries from a.sessions or a.warned when sessions end.
- **Fix:** Added `RemoveSession()` to Analyzer and BudgetEnforcer.

### M8: API server goroutine not tracked by WaitGroup
- **File:** `internal/daemon/daemon.go:110-114`
- **Status:** CONFIRMED | FIXED
- **Description:** API server goroutine not added to wg, so Shutdown() doesn't wait for it.
- **Fix:** Added wg.Add(1) and defer wg.Done().

### M9: Duplicate socket path implementations
- **File:** `internal/daemon/daemon.go:329`, `internal/api/server.go:77`
- **Status:** CONFIRMED | FIXED
- **Description:** Two independent implementations of socket path logic. Could diverge.
- **Fix:** Daemon uses api.SocketPath(). Removed daemon.socketPath().

### M9b: Auto-registration race can overwrite properly-discovered session (found in pass 2)
- **File:** `internal/daemon/daemon.go:246`, `internal/discovery/discovery.go:39`
- **Status:** CONFIRMED | FIXED
- **Description:** `processEvents` auto-registers unknown sessions via `registry.Add`, which unconditionally overwrites. If `rediscoveryLoop` adds a properly-discovered session (with PID) between the `Get` and `Add` calls, the proper session is overwritten with a PID-less auto-registered one.
- **Fix:** Added `Registry.TryAdd()` that only adds if session doesn't already exist.

### M10: HTTP 400 for all resume errors (should be 404 for not found)
- **File:** `internal/api/handlers.go:57`
- **Status:** CONFIRMED | FIXED
- **Description:** "session not found" returns 400 instead of 404.
- **Fix:** Returns HTTP 404 when error message contains "not found".

### M11: Config env var parse errors silently ignored
- **File:** `internal/config/config.go:100-108`
- **Status:** CONFIRMED | FIXED
- **Description:** `fmt.Sscanf` errors discarded. User sets `LOOPGUARD_BUDGET_PER_SESSION=abc`, gets no feedback.
- **Fix:** Use strconv.ParseFloat, log warning on parse failure.

### M12: YAML null sections zero out default config values
- **File:** `internal/config/config.go:91`
- **Status:** CONFIRMED | FIXED
- **Description:** A bare `budget:` key with no values zeros all budget fields, potentially disabling safety limits.
- **Fix:** `applyDefaults()` restores structural defaults (spin thresholds, paths, log level) while allowing budget values of 0 to mean "disabled".

### M13: HTTP client has no timeout
- **File:** `internal/cli/client.go:14`
- **Status:** CONFIRMED | FIXED
- **Description:** CLI commands hang forever if daemon is unresponsive.
- **Fix:** Added 10s timeout.

### M14: Kill follow-up fails to clean up orphaned child processes
- **File:** `internal/enforcer/enforcer.go:108`
- **Status:** CONFIRMED | FIXED
- **Description:** If group leader dies but children survive, SIGKILL on leader PID fails, leaving orphans.
- **Fix:** SIGKILL follow-up now targets `-pgid` (entire process group) instead of individual PID.

### M15: Sentinel file write follows symlinks
- **File:** `internal/enforcer/sentinel.go:11`
- **Status:** CONFIRMED | FIXED
- **Description:** `os.WriteFile` follows symlinks. Attacker could redirect sentinel write to overwrite arbitrary files.
- **Fix:** Added Lstat check before WriteFile.

### M16: Custom sessions always marked Active:true
- **File:** `internal/discovery/custom.go:70`
- **Status:** CONFIRMED | FIXED
- **Description:** No liveness check for custom sessions. Stale files reported as active.
- **Fix:** Custom sessions use `lsof` to check if a process has the JSONL file open.

### M17: Custom discoverer BasePath() only returns first pattern
- **File:** `internal/discovery/custom.go:28-32`
- **Status:** CONFIRMED | FIXED
- **Description:** Only the first custom path gets fsnotify watching.
- **Fix:** Each custom path creates a separate CustomDiscoverer instance, each returning its own BasePath for watching.

### M18: Codex sessions missing ProjectDir
- **File:** `internal/discovery/codex.go:60`
- **Status:** CONFIRMED | FIXED
- **Description:** ProjectDir is never set for codex sessions, breaking sentinel fallback.
- **Fix:** Codex sessions set ProjectDir from the session path directory.

---

## Low

### L1: Resume prefix match could match wrong session
- **File:** `internal/cli/resume.go:27-30`
- **Status:** CONFIRMED | FIXED
- **Description:** First prefix match wins. If multiple sessions share a prefix, wrong one is resumed.
- **Fix:** Collects all matches; rejects ambiguous prefixes with a user-facing error listing the matches.

### L2: History/emitter TOCTOU symlink check
- **File:** `internal/daemon/history.go:45`, `internal/ltf/emitter.go:148`
- **Status:** CONFIRMED | FIXED
- **Description:** Lstat check then OpenFile has a race window for symlink swap.
- **Fix:** History file opened with `O_NOFOLLOW` syscall flag, eliminating the TOCTOU window.

### L3: HTTP server has no read/write/idle timeouts
- **File:** `internal/api/server.go:59`
- **Status:** CONFIRMED | FIXED
- **Description:** A misbehaving client could exhaust file descriptors.
- **Fix:** Added ReadTimeout: 10s, WriteTimeout: 10s, IdleTimeout: 60s.

### L4: Status variable shadows outer variable
- **File:** `internal/cli/status.go:51`
- **Status:** CONFIRMED | FIXED
- **Description:** `status := "running"` shadows outer `*api.StatusResponse`.
- **Fix:** Renamed inner variable to `state`.

### L5: StartedAt set to ModTime not actual session start
- **File:** `internal/discovery/claude.go:74`
- **Status:** CONFIRMED | FIXED
- **Description:** ModTime reflects last write, not session creation time.
- **Fix:** Reads the timestamp from the first JSONL line; falls back to ModTime if parse fails.

### L6: AppleScript %q may produce escape sequences osascript doesn't understand
- **File:** `internal/notify/notify.go:62`
- **Status:** CONFIRMED | FIXED
- **Description:** Go's %q produces \u escapes for non-ASCII; AppleScript may not parse them.
- **Fix:** Replaced `%q` with `escapeAppleScript()` that only escapes backslashes and double quotes, passing UTF-8 through literally.
