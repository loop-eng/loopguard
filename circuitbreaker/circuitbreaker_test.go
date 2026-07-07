package circuitbreaker

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

var errSimulated = errors.New("simulated failure")

// tripBreaker drives the breaker through enough consecutive failures to
// trip it from Closed to Open.
func tripBreaker(t *testing.T, cb *CircuitBreaker, failures int64) {
	t.Helper()
	for i := int64(0); i < failures; i++ {
		if err := cb.ShouldAllow(); err != nil {
			t.Fatalf("ShouldAllow() failed on attempt %d: %v", i, err)
		}
		cb.RecordFailure()
	}
}

// ---------------------------------------------------------------------------
// State machine tests
// ---------------------------------------------------------------------------

func TestNewCircuitBreaker_DefaultsClosed(t *testing.T) {
	cb := New(Config{})

	if cb.CurrentState() != StateClosed {
		t.Errorf("expected Closed, got %s", cb.CurrentState())
	}
	if cb.CurrentIntervention() != InterventionNone {
		t.Errorf("expected InterventionNone, got %s", cb.CurrentIntervention())
	}
	if cb.IsKilled() {
		t.Error("should not be killed initially")
	}
	if cb.IsPaused() {
		t.Error("should not be paused initially")
	}
}

func TestClosedToOpen_OnFailureThreshold(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 3,
		KillThreshold:    100, // high so we don't trigger kill
		PauseThreshold:   100,
		WarnThreshold:    100,
	})

	// Record 2 failures — should stay closed.
	tripBreaker(t, cb, 2)
	if cb.CurrentState() != StateClosed {
		t.Errorf("expected Closed after 2 failures, got %s", cb.CurrentState())
	}

	// 3rd failure trips the breaker.
	if err := cb.ShouldAllow(); err != nil {
		t.Fatalf("ShouldAllow before 3rd failure: %v", err)
	}
	cb.RecordFailure()

	if cb.CurrentState() != StateOpen {
		t.Errorf("expected Open after 3 failures, got %s", cb.CurrentState())
	}
}

func TestOpenRejectsRequests(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 2,
		OpenCooldown:     1 * time.Hour, // long cooldown
		KillThreshold:    100,
		PauseThreshold:   100,
		WarnThreshold:    100,
	})

	tripBreaker(t, cb, 2)

	err := cb.ShouldAllow()
	if !errors.Is(err, ErrBreakerOpen) {
		t.Errorf("expected ErrBreakerOpen, got %v", err)
	}
}

func TestOpenToHalfOpen_AfterCooldown(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 2,
		OpenCooldown:     50 * time.Millisecond,
		KillThreshold:    100,
		PauseThreshold:   100,
		WarnThreshold:    100,
	})

	tripBreaker(t, cb, 2)

	// Wait for cooldown.
	time.Sleep(60 * time.Millisecond)

	// ShouldAllow should now transition to HalfOpen and allow.
	if err := cb.ShouldAllow(); err != nil {
		t.Fatalf("expected nil after cooldown, got %v", err)
	}

	if cb.CurrentState() != StateHalfOpen {
		t.Errorf("expected HalfOpen, got %s", cb.CurrentState())
	}
}

func TestHalfOpenToClosed_OnSuccessThreshold(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 2,
		SuccessThreshold: 2,
		OpenCooldown:     1 * time.Millisecond,
		MaxRetries:       5,
		KillThreshold:    100,
		PauseThreshold:   100,
		WarnThreshold:    100,
	})

	tripBreaker(t, cb, 2)
	time.Sleep(5 * time.Millisecond)

	// First probe.
	if err := cb.ShouldAllow(); err != nil {
		t.Fatalf("first probe: %v", err)
	}
	cb.RecordSuccess()

	if cb.CurrentState() != StateHalfOpen {
		t.Errorf("should still be HalfOpen after 1 success, got %s", cb.CurrentState())
	}

	// Second probe.
	if err := cb.ShouldAllow(); err != nil {
		t.Fatalf("second probe: %v", err)
	}
	cb.RecordSuccess()

	if cb.CurrentState() != StateClosed {
		t.Errorf("expected Closed after 2 successes in HalfOpen, got %s", cb.CurrentState())
	}
}

func TestHalfOpenToOpen_OnFailure(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 2,
		OpenCooldown:     1 * time.Millisecond,
		KillThreshold:    100,
		PauseThreshold:   100,
		WarnThreshold:    100,
	})

	tripBreaker(t, cb, 2)
	time.Sleep(5 * time.Millisecond)

	// Allow one probe.
	if err := cb.ShouldAllow(); err != nil {
		t.Fatalf("probe: %v", err)
	}

	// Fail it.
	cb.RecordFailure()

	if cb.CurrentState() != StateOpen {
		t.Errorf("expected Open after HalfOpen failure, got %s", cb.CurrentState())
	}
}

// ---------------------------------------------------------------------------
// Retry budget tests
// ---------------------------------------------------------------------------

func TestRetryBudget_ExhaustedInHalfOpen(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 2,
		OpenCooldown:     1 * time.Millisecond,
		MaxRetries:       3,
		KillThreshold:    100,
		PauseThreshold:   100,
		WarnThreshold:    100,
	})

	tripBreaker(t, cb, 2)
	time.Sleep(5 * time.Millisecond)

	// Use all 3 retries (the first ShouldAllow transitions to HalfOpen
	// and counts as retry #1).
	for i := 0; i < 3; i++ {
		if err := cb.ShouldAllow(); err != nil {
			t.Fatalf("retry %d: unexpected error: %v", i, err)
		}
		// Don't record anything — just burn retries.
	}

	// 4th attempt should be rejected.
	err := cb.ShouldAllow()
	if !errors.Is(err, ErrRetryBudgetExhausted) {
		t.Errorf("expected ErrRetryBudgetExhausted, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Cooldown scaling tests
// ---------------------------------------------------------------------------

func TestCooldownMultiplier(t *testing.T) {
	cb := New(Config{
		FailureThreshold:   2,
		OpenCooldown:       100 * time.Millisecond,
		CooldownMultiplier: 2.0,
		MaxCooldown:        10 * time.Second,
		KillThreshold:      100,
		PauseThreshold:     100,
		WarnThreshold:      100,
	})

	// First trip: cooldown = 100ms.
	tripBreaker(t, cb, 2)
	if cb.CurrentCooldown() != 100*time.Millisecond {
		t.Errorf("first trip cooldown: expected 100ms, got %s", cb.CurrentCooldown())
	}

	// Wait for cooldown, transition to HalfOpen, then fail to reopen.
	time.Sleep(110 * time.Millisecond)
	if err := cb.ShouldAllow(); err != nil {
		t.Fatalf("probe after first trip: %v", err)
	}
	cb.RecordFailure() // trips again

	// Second trip: cooldown = 100ms * 2.0 = 200ms.
	expected := 200 * time.Millisecond
	got := cb.CurrentCooldown()
	if got != expected {
		t.Errorf("second trip cooldown: expected %s, got %s", expected, got)
	}
}

func TestCooldownCappedAtMax(t *testing.T) {
	cb := New(Config{
		FailureThreshold:   1,
		OpenCooldown:       100 * time.Millisecond,
		CooldownMultiplier: 100.0, // aggressive multiplier
		MaxCooldown:        500 * time.Millisecond,
		KillThreshold:      100,
		PauseThreshold:     100,
		WarnThreshold:      100,
	})

	// First trip.
	tripBreaker(t, cb, 1)

	// Recover and trip again.
	time.Sleep(110 * time.Millisecond)
	if err := cb.ShouldAllow(); err != nil {
		t.Fatal(err)
	}
	cb.RecordFailure()

	got := cb.CurrentCooldown()
	maxCooldown := 500 * time.Millisecond
	if got > maxCooldown {
		t.Errorf("cooldown %s exceeds max %s", got, maxCooldown)
	}
}

// ---------------------------------------------------------------------------
// Escalating intervention tests
// ---------------------------------------------------------------------------

func TestWarnIntervention(t *testing.T) {
	var warnFired bool

	cb := New(Config{
		FailureThreshold: 10, // high, so the breaker doesn't trip first
		WarnThreshold:    3,
		PauseThreshold:   100,
		KillThreshold:    100,
		OnIntervention: func(e InterventionEvent) {
			if e.Level == InterventionWarn {
				warnFired = true
			}
		},
	})

	for i := 0; i < 3; i++ {
		cb.ShouldAllow()
		cb.RecordFailure()
	}

	if !warnFired {
		t.Error("expected Warn intervention to fire at 3 failures")
	}
	if cb.CurrentIntervention() != InterventionWarn {
		t.Errorf("expected InterventionWarn, got %s", cb.CurrentIntervention())
	}
}

func TestPauseIntervention(t *testing.T) {
	var pauseFired bool

	cb := New(Config{
		FailureThreshold: 100,
		WarnThreshold:    2,
		PauseThreshold:   5,
		KillThreshold:    100,
		OnIntervention: func(e InterventionEvent) {
			if e.Level == InterventionPause {
				pauseFired = true
			}
		},
	})

	for i := 0; i < 5; i++ {
		cb.ShouldAllow()
		cb.RecordFailure()
	}

	if !pauseFired {
		t.Error("expected Pause intervention to fire at 5 failures")
	}
	if !cb.IsPaused() {
		t.Error("breaker should be paused")
	}
	if cb.CurrentIntervention() != InterventionPause {
		t.Errorf("expected InterventionPause, got %s", cb.CurrentIntervention())
	}

	// ShouldAllow should return ErrBreakerPaused.
	err := cb.ShouldAllow()
	if !errors.Is(err, ErrBreakerPaused) {
		t.Errorf("expected ErrBreakerPaused, got %v", err)
	}
}

func TestKillIntervention(t *testing.T) {
	var killFired bool

	cb := New(Config{
		FailureThreshold: 100,
		WarnThreshold:    2,
		PauseThreshold:   5,
		KillThreshold:    8,
		OnIntervention: func(e InterventionEvent) {
			if e.Level == InterventionKill {
				killFired = true
			}
		},
	})

	// Drive past pause (which sets paused=true), then resume to keep going.
	for i := 0; i < 5; i++ {
		cb.ShouldAllow()
		cb.RecordFailure()
	}
	cb.Resume() // clear pause so we can keep recording

	for i := 0; i < 3; i++ {
		cb.ShouldAllow()
		cb.RecordFailure()
	}

	if !killFired {
		t.Error("expected Kill intervention to fire at 8 failures")
	}
	if !cb.IsKilled() {
		t.Error("breaker should be killed")
	}

	err := cb.ShouldAllow()
	if !errors.Is(err, ErrBreakerKilled) {
		t.Errorf("expected ErrBreakerKilled, got %v", err)
	}
}

func TestEscalationOrder(t *testing.T) {
	var levels []InterventionLevel

	cb := New(Config{
		FailureThreshold: 100,
		WarnThreshold:    2,
		PauseThreshold:   4,
		KillThreshold:    6,
		OnIntervention: func(e InterventionEvent) {
			levels = append(levels, e.Level)
		},
	})

	// 2 failures -> Warn
	for i := 0; i < 2; i++ {
		cb.ShouldAllow()
		cb.RecordFailure()
	}

	// 2 more -> Pause (total 4)
	cb.ShouldAllow()
	cb.RecordFailure()
	cb.Resume() // clear pause from threshold check at failure 4
	cb.ShouldAllow()
	cb.RecordFailure()
	cb.Resume()

	// 2 more -> Kill (total 6)
	cb.ShouldAllow()
	cb.RecordFailure()
	cb.Resume()
	cb.ShouldAllow()
	cb.RecordFailure()

	if len(levels) < 3 {
		t.Fatalf("expected at least 3 interventions, got %d: %v", len(levels), levels)
	}

	if levels[0] != InterventionWarn {
		t.Errorf("first intervention should be Warn, got %s", levels[0])
	}
	if levels[1] != InterventionPause {
		t.Errorf("second intervention should be Pause, got %s", levels[1])
	}
	if levels[len(levels)-1] != InterventionKill {
		t.Errorf("last intervention should be Kill, got %s", levels[len(levels)-1])
	}
}

// ---------------------------------------------------------------------------
// Human-in-the-loop tests
// ---------------------------------------------------------------------------

func TestHumanApproval_Approved(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 100,
		WarnThreshold:    2,
		PauseThreshold:   3,
		KillThreshold:    100,
		HumanApproval: func(e InterventionEvent) bool {
			return true // human approves
		},
	})

	for i := 0; i < 3; i++ {
		cb.ShouldAllow()
		cb.RecordFailure()
	}

	// After human approves, the breaker should be resumed.
	if cb.IsPaused() {
		t.Error("breaker should not be paused after human approval")
	}
	if cb.IsKilled() {
		t.Error("breaker should not be killed after human approval")
	}
	if cb.CurrentState() != StateClosed {
		t.Errorf("expected Closed after approval, got %s", cb.CurrentState())
	}
}

func TestHumanApproval_Rejected(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 100,
		WarnThreshold:    2,
		PauseThreshold:   3,
		KillThreshold:    100,
		HumanApproval: func(e InterventionEvent) bool {
			return false // human rejects
		},
	})

	for i := 0; i < 3; i++ {
		cb.ShouldAllow()
		cb.RecordFailure()
	}

	// After human rejects, the breaker should be killed.
	if !cb.IsKilled() {
		t.Error("breaker should be killed after human rejection")
	}

	err := cb.ShouldAllow()
	if !errors.Is(err, ErrBreakerKilled) {
		t.Errorf("expected ErrBreakerKilled after rejection, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Resume and Reset tests
// ---------------------------------------------------------------------------

func TestResume_ClearsPause(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 100,
		PauseThreshold:   3,
		KillThreshold:    100,
	})

	for i := 0; i < 3; i++ {
		cb.ShouldAllow()
		cb.RecordFailure()
	}

	if !cb.IsPaused() {
		t.Fatal("expected paused")
	}

	cb.Resume()

	if cb.IsPaused() {
		t.Error("should not be paused after Resume")
	}
	if cb.CurrentState() != StateClosed {
		t.Errorf("expected Closed after Resume, got %s", cb.CurrentState())
	}

	// Should be able to proceed.
	if err := cb.ShouldAllow(); err != nil {
		t.Errorf("ShouldAllow after Resume: %v", err)
	}
}

func TestReset_ClearsEverything(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 2,
		KillThreshold:    5,
		OpenCooldown:     1 * time.Hour,
	})

	// Drive to kill.
	for i := 0; i < 5; i++ {
		cb.ShouldAllow()
		cb.RecordFailure()
	}

	if !cb.IsKilled() {
		t.Fatal("expected killed")
	}

	cb.Reset()

	if cb.IsKilled() {
		t.Error("should not be killed after Reset")
	}
	if cb.IsPaused() {
		t.Error("should not be paused after Reset")
	}
	if cb.CurrentState() != StateClosed {
		t.Errorf("expected Closed after Reset, got %s", cb.CurrentState())
	}
	if cb.TripCount() != 0 {
		t.Errorf("expected 0 trip count after Reset, got %d", cb.TripCount())
	}
	counts := cb.GetCounts()
	if counts.Failures != 0 || counts.Successes != 0 || counts.Requests != 0 {
		t.Errorf("expected zeroed counts after Reset, got %+v", counts)
	}
	if cb.CurrentIntervention() != InterventionNone {
		t.Errorf("expected InterventionNone after Reset, got %s", cb.CurrentIntervention())
	}
}

// ---------------------------------------------------------------------------
// WaitForResume tests
// ---------------------------------------------------------------------------

func TestWaitForResume_ReturnsImmediatelyIfNotPaused(t *testing.T) {
	cb := New(Config{})
	done := make(chan struct{})
	go func() {
		cb.WaitForResume()
		close(done)
	}()

	select {
	case <-done:
		// Good, returned immediately.
	case <-time.After(100 * time.Millisecond):
		t.Error("WaitForResume did not return immediately when not paused")
	}
}

func TestWaitForResume_BlocksUntilResumed(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 100,
		PauseThreshold:   2,
		KillThreshold:    100,
	})

	cb.ShouldAllow()
	cb.RecordFailure()
	cb.ShouldAllow()
	cb.RecordFailure()

	if !cb.IsPaused() {
		t.Fatal("expected paused")
	}

	done := make(chan struct{})
	go func() {
		cb.WaitForResume()
		close(done)
	}()

	// Should not return yet.
	select {
	case <-done:
		t.Fatal("WaitForResume returned before Resume was called")
	case <-time.After(50 * time.Millisecond):
		// Good, still blocking.
	}

	cb.Resume()

	select {
	case <-done:
		// Good, unblocked after Resume.
	case <-time.After(100 * time.Millisecond):
		t.Error("WaitForResume did not unblock after Resume")
	}
}

// ---------------------------------------------------------------------------
// Execute tests
// ---------------------------------------------------------------------------

func TestExecute_SuccessPath(t *testing.T) {
	cb := New(Config{})

	err := cb.Execute(func() error {
		return nil
	})

	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}

	counts := cb.GetCounts()
	if counts.Successes != 1 {
		t.Errorf("expected 1 success, got %d", counts.Successes)
	}
}

func TestExecute_FailurePath(t *testing.T) {
	cb := New(Config{KillThreshold: 100, PauseThreshold: 100})

	err := cb.Execute(func() error {
		return errSimulated
	})

	if !errors.Is(err, errSimulated) {
		t.Errorf("expected errSimulated, got %v", err)
	}

	counts := cb.GetCounts()
	if counts.Failures != 1 {
		t.Errorf("expected 1 failure, got %d", counts.Failures)
	}
}

func TestExecute_BreakerOpenSkipsFunction(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 2,
		OpenCooldown:     1 * time.Hour,
		KillThreshold:    100,
		PauseThreshold:   100,
		WarnThreshold:    100,
	})

	tripBreaker(t, cb, 2)

	called := false
	err := cb.Execute(func() error {
		called = true
		return nil
	})

	if !errors.Is(err, ErrBreakerOpen) {
		t.Errorf("expected ErrBreakerOpen, got %v", err)
	}
	if called {
		t.Error("function should not have been called when breaker is open")
	}
}

// ---------------------------------------------------------------------------
// OnStateChange callback tests
// ---------------------------------------------------------------------------

func TestOnStateChange_Callbacks(t *testing.T) {
	var transitions []struct{ from, to State }

	cb := New(Config{
		FailureThreshold: 2,
		SuccessThreshold: 1,
		OpenCooldown:     1 * time.Millisecond,
		KillThreshold:    100,
		PauseThreshold:   100,
		WarnThreshold:    100,
		OnStateChange: func(from, to State) {
			transitions = append(transitions, struct{ from, to State }{from, to})
		},
	})

	// Trip: Closed -> Open.
	tripBreaker(t, cb, 2)

	// Wait for cooldown: Open -> HalfOpen.
	time.Sleep(5 * time.Millisecond)
	cb.ShouldAllow()

	// Succeed: HalfOpen -> Closed.
	cb.RecordSuccess()

	if len(transitions) != 3 {
		t.Fatalf("expected 3 transitions, got %d: %+v", len(transitions), transitions)
	}
	if transitions[0].from != StateClosed || transitions[0].to != StateOpen {
		t.Errorf("transition 0: expected Closed->Open, got %s->%s", transitions[0].from, transitions[0].to)
	}
	if transitions[1].from != StateOpen || transitions[1].to != StateHalfOpen {
		t.Errorf("transition 1: expected Open->HalfOpen, got %s->%s", transitions[1].from, transitions[1].to)
	}
	if transitions[2].from != StateHalfOpen || transitions[2].to != StateClosed {
		t.Errorf("transition 2: expected HalfOpen->Closed, got %s->%s", transitions[2].from, transitions[2].to)
	}
}

// ---------------------------------------------------------------------------
// ClearInterval tests
// ---------------------------------------------------------------------------

func TestClearInterval_ResetsCounts(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 10,
		ClearInterval:    50 * time.Millisecond,
		KillThreshold:    100,
		PauseThreshold:   100,
		WarnThreshold:    100,
	})

	// Record some failures.
	for i := 0; i < 5; i++ {
		cb.ShouldAllow()
		cb.RecordFailure()
	}

	counts := cb.GetCounts()
	if counts.Failures != 5 {
		t.Fatalf("expected 5 failures, got %d", counts.Failures)
	}

	// Wait for clear interval.
	time.Sleep(60 * time.Millisecond)

	// ShouldAllow triggers the clear.
	cb.ShouldAllow()

	counts = cb.GetCounts()
	if counts.Failures != 0 {
		t.Errorf("expected 0 failures after clear, got %d", counts.Failures)
	}
}

// ---------------------------------------------------------------------------
// Snapshot and Summary tests
// ---------------------------------------------------------------------------

func TestSnapshot(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 5,
		OpenCooldown:     30 * time.Second,
	})

	cb.ShouldAllow()
	cb.RecordSuccess()
	cb.ShouldAllow()
	cb.RecordFailure()

	snap := cb.TakeSnapshot()
	if snap.State != StateClosed {
		t.Errorf("expected Closed, got %s", snap.State)
	}
	if snap.Counts.Requests != 2 {
		t.Errorf("expected 2 requests, got %d", snap.Counts.Requests)
	}
	if snap.Counts.Successes != 1 {
		t.Errorf("expected 1 success, got %d", snap.Counts.Successes)
	}
	if snap.Counts.Failures != 1 {
		t.Errorf("expected 1 failure, got %d", snap.Counts.Failures)
	}
}

func TestSummary_NotEmpty(t *testing.T) {
	cb := New(Config{})
	s := cb.Summary()
	if len(s) == 0 {
		t.Error("summary should not be empty")
	}
}

// ---------------------------------------------------------------------------
// CooldownRemaining tests
// ---------------------------------------------------------------------------

func TestCooldownRemaining_ZeroWhenClosed(t *testing.T) {
	cb := New(Config{})
	if cb.CooldownRemaining() != 0 {
		t.Errorf("expected 0 remaining when closed, got %s", cb.CooldownRemaining())
	}
}

func TestCooldownRemaining_DecaysInOpenState(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 1,
		OpenCooldown:     200 * time.Millisecond,
		KillThreshold:    100,
		PauseThreshold:   100,
		WarnThreshold:    100,
	})

	tripBreaker(t, cb, 1)
	rem1 := cb.CooldownRemaining()

	time.Sleep(50 * time.Millisecond)
	rem2 := cb.CooldownRemaining()

	if rem2 >= rem1 {
		t.Errorf("cooldown should decay: %s then %s", rem1, rem2)
	}
}

// ---------------------------------------------------------------------------
// Stringer tests
// ---------------------------------------------------------------------------

func TestStateString(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateClosed, "Closed"},
		{StateOpen, "Open"},
		{StateHalfOpen, "HalfOpen"},
		{State(99), "Unknown(99)"},
	}
	for _, tc := range tests {
		if got := tc.state.String(); got != tc.want {
			t.Errorf("State(%d).String() = %q, want %q", tc.state, got, tc.want)
		}
	}
}

func TestInterventionLevelString(t *testing.T) {
	tests := []struct {
		level InterventionLevel
		want  string
	}{
		{InterventionNone, "None"},
		{InterventionWarn, "Warn"},
		{InterventionPause, "Pause"},
		{InterventionKill, "Kill"},
		{InterventionLevel(99), "Unknown(99)"},
	}
	for _, tc := range tests {
		if got := tc.level.String(); got != tc.want {
			t.Errorf("InterventionLevel(%d).String() = %q, want %q", tc.level, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Concurrent access tests
// ---------------------------------------------------------------------------

func TestConcurrentAccess(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 100,
		OpenCooldown:     10 * time.Millisecond,
		KillThreshold:    10000,
		PauseThreshold:   10000,
		WarnThreshold:    10000,
	})

	var wg sync.WaitGroup
	iterations := 500

	// Writer: record failures.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			cb.ShouldAllow()
			cb.RecordFailure()
		}
	}()

	// Writer: record successes.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			cb.ShouldAllow()
			cb.RecordSuccess()
		}
	}()

	// Reader: take snapshots.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = cb.TakeSnapshot()
			_ = cb.CurrentState()
			_ = cb.CurrentIntervention()
			_ = cb.IsKilled()
			_ = cb.IsPaused()
			_ = cb.CooldownRemaining()
			_ = cb.GetCounts()
		}
	}()

	wg.Wait()

	counts := cb.GetCounts()
	if counts.Requests != int64(iterations*2) {
		t.Errorf("expected %d total requests, got %d", iterations*2, counts.Requests)
	}
}

func TestConcurrentExecute(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 1000,
		KillThreshold:    10000,
		PauseThreshold:   10000,
		WarnThreshold:    10000,
	})

	var wg sync.WaitGroup
	var successCount atomic.Int64
	var failCount atomic.Int64

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			err := cb.Execute(func() error {
				if n%3 == 0 {
					return errSimulated
				}
				return nil
			})
			if err == nil {
				successCount.Add(1)
			} else if errors.Is(err, errSimulated) {
				failCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	total := successCount.Load() + failCount.Load()
	if total != 100 {
		t.Errorf("expected 100 total operations, got %d", total)
	}
}

// ---------------------------------------------------------------------------
// Counts struct tests
// ---------------------------------------------------------------------------

func TestCounts_Reset(t *testing.T) {
	c := Counts{
		Requests:             10,
		Successes:            5,
		Failures:             5,
		ConsecutiveSuccesses: 2,
		ConsecutiveFailures:  3,
		RetriesUsed:          2,
	}
	c.Reset()

	if c.Requests != 0 || c.Successes != 0 || c.Failures != 0 ||
		c.ConsecutiveSuccesses != 0 || c.ConsecutiveFailures != 0 ||
		c.RetriesUsed != 0 {
		t.Errorf("counts not fully reset: %+v", c)
	}
}

// ---------------------------------------------------------------------------
// Edge case tests
// ---------------------------------------------------------------------------

func TestRecordSuccess_WhenKilled_Noop(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 1,
		KillThreshold:    1,
	})

	cb.ShouldAllow()
	cb.RecordFailure()

	if !cb.IsKilled() {
		t.Fatal("expected killed")
	}

	cb.RecordSuccess() // should be a no-op

	if !cb.IsKilled() {
		t.Error("kill flag should not be cleared by RecordSuccess")
	}
}

func TestRecordFailure_WhenKilled_Noop(t *testing.T) {
	cb := New(Config{
		FailureThreshold: 1,
		KillThreshold:    1,
	})

	cb.ShouldAllow()
	cb.RecordFailure()

	countsBefore := cb.GetCounts()

	cb.RecordFailure() // should be a no-op

	countsAfter := cb.GetCounts()
	if countsAfter.Failures != countsBefore.Failures {
		t.Errorf("failure count should not change after kill: before=%d, after=%d",
			countsBefore.Failures, countsAfter.Failures)
	}
}

func TestResume_WhenNotPaused_Noop(t *testing.T) {
	cb := New(Config{})
	cb.Resume() // should not panic or change state

	if cb.CurrentState() != StateClosed {
		t.Errorf("expected Closed, got %s", cb.CurrentState())
	}
}

func TestMultipleResets(t *testing.T) {
	cb := New(Config{FailureThreshold: 1, KillThreshold: 2})

	cb.ShouldAllow()
	cb.RecordFailure()
	cb.ShouldAllow()
	cb.RecordFailure()

	cb.Reset()
	cb.Reset() // second reset should be safe

	if cb.CurrentState() != StateClosed {
		t.Errorf("expected Closed after double reset, got %s", cb.CurrentState())
	}
}

// ---------------------------------------------------------------------------
// Full lifecycle integration test
// ---------------------------------------------------------------------------

func TestFullLifecycle(t *testing.T) {
	// Simulate a complete lifecycle:
	// 1. Normal operation (Closed)
	// 2. Failures accumulate -> Warn fires
	// 3. More failures -> breaker trips (Open)
	// 4. Cooldown elapses -> HalfOpen
	// 5. Probe succeeds -> Closed
	// 6. More failures -> Pause fires
	// 7. Resume
	// 8. Even more failures -> Kill fires
	// 9. Reset -> back to normal

	var interventionLog []InterventionLevel
	var stateLog []struct{ from, to State }

	cb := New(Config{
		FailureThreshold: 3,
		SuccessThreshold: 1,
		OpenCooldown:     10 * time.Millisecond,
		MaxRetries:       5,
		WarnThreshold:    2,
		PauseThreshold:   6,
		KillThreshold:    9,
		OnIntervention: func(e InterventionEvent) {
			interventionLog = append(interventionLog, e.Level)
		},
		OnStateChange: func(from, to State) {
			stateLog = append(stateLog, struct{ from, to State }{from, to})
		},
	})

	// Phase 1: Normal operation.
	cb.ShouldAllow()
	cb.RecordSuccess()
	if cb.CurrentState() != StateClosed {
		t.Fatal("phase 1: expected Closed")
	}

	// Phase 2: Accumulate failures to trigger Warn (threshold=2).
	cb.ShouldAllow()
	cb.RecordFailure() // failure #1
	cb.ShouldAllow()
	cb.RecordFailure() // failure #2 -> Warn

	if cb.CurrentIntervention() != InterventionWarn {
		t.Fatalf("phase 2: expected Warn, got %s", cb.CurrentIntervention())
	}

	// Phase 3: One more failure trips the breaker (threshold=3).
	cb.ShouldAllow()
	cb.RecordFailure() // failure #3 -> Closed->Open

	if cb.CurrentState() != StateOpen {
		t.Fatalf("phase 3: expected Open, got %s", cb.CurrentState())
	}

	// Phase 4: Wait for cooldown.
	time.Sleep(15 * time.Millisecond)
	if err := cb.ShouldAllow(); err != nil {
		t.Fatalf("phase 4: %v", err)
	}
	if cb.CurrentState() != StateHalfOpen {
		t.Fatalf("phase 4: expected HalfOpen, got %s", cb.CurrentState())
	}

	// Phase 5: Probe succeeds -> Closed.
	cb.RecordSuccess()
	if cb.CurrentState() != StateClosed {
		t.Fatalf("phase 5: expected Closed, got %s", cb.CurrentState())
	}

	// Phase 6: More failures to trigger Pause (threshold=6, we have 3 so far).
	for i := 0; i < 3; i++ {
		cb.ShouldAllow()
		cb.RecordFailure() // failures #4, #5, #6
	}

	if !cb.IsPaused() {
		t.Fatal("phase 6: expected paused")
	}

	// Phase 7: Resume.
	cb.Resume()
	if cb.IsPaused() {
		t.Fatal("phase 7: expected not paused after Resume")
	}

	// Phase 8: More failures to trigger Kill (threshold=9, we have 6 so far,
	// but Resume resets counts, so we need 9 fresh failures).
	for i := 0; i < 9; i++ {
		cb.ShouldAllow()
		cb.RecordFailure()
	}

	if !cb.IsKilled() {
		t.Fatal("phase 8: expected killed")
	}

	// Phase 9: Reset.
	cb.Reset()
	if cb.IsKilled() {
		t.Fatal("phase 9: expected not killed after Reset")
	}
	if cb.CurrentState() != StateClosed {
		t.Fatalf("phase 9: expected Closed, got %s", cb.CurrentState())
	}

	// Verify intervention log has all three levels.
	hasWarn, hasPause, hasKill := false, false, false
	for _, l := range interventionLog {
		switch l {
		case InterventionWarn:
			hasWarn = true
		case InterventionPause:
			hasPause = true
		case InterventionKill:
			hasKill = true
		}
	}
	if !hasWarn {
		t.Error("missing Warn in intervention log")
	}
	if !hasPause {
		t.Error("missing Pause in intervention log")
	}
	if !hasKill {
		t.Error("missing Kill in intervention log")
	}
}
