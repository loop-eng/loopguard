// Package circuitbreaker implements a circuit breaker with escalating
// interventions (Warn -> Pause -> Kill) for AI agent loop supervision.
//
// The circuit breaker is a state machine with three states:
//
//   - Closed: Normal operation. Requests flow through. Failures are counted.
//     When failures exceed a threshold, the breaker trips to Open.
//   - Open: The breaker has tripped. All requests are rejected immediately.
//     After a cooldown period, the breaker transitions to HalfOpen.
//   - HalfOpen: Recovery testing. A limited number of probe requests are
//     allowed through. If they succeed, the breaker closes. If any fail,
//     the breaker reopens with an extended cooldown.
//
// On top of the classic three-state model, this implementation layers an
// escalating intervention system inspired by production AI agent guardrails:
//
//   - Warn: The agent has accumulated enough failures to warrant attention.
//     A warning callback fires, but execution continues. This is the
//     "yellow light" phase.
//   - Pause: Failures have exceeded the pause threshold. The breaker enters
//     a paused state and optionally blocks until a human approves resumption
//     (human-in-the-loop pattern). This maps to SIGSTOP in LoopGuard.
//   - Kill: The situation is unrecoverable. The breaker fires a kill callback
//     and permanently opens. This maps to SIGTERM/SIGKILL in LoopGuard.
//
// Design informed by:
//   - Michael Nygard, "Release It!" — original circuit breaker pattern
//   - sony/gobreaker — production Go circuit breaker (Settings/Counts structs)
//   - Martin Fowler's CircuitBreaker bliki entry
//   - Google SRE Book — retry budgets, handling overload
//   - Alephant's Budget Circuit Breaker — Alert -> Throttle -> Kill
//   - Portal26 Adaptive Safeguards — throttle/pause/terminate
//   - EU AI Act Article 14 — human oversight with immutable logs
//   - Microsoft Azure circuit breaker pattern guidance
//   - mercari/go-circuitbreaker — context-aware design
//   - octo/retry — retry budget with Rate and Ratio fields
//   - k4ties/cooldown — cooldown timer with pause/resume/renew
//
// Thread safety: All exported methods are safe for concurrent use.
// Internal state is guarded by sync.Mutex.
package circuitbreaker

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// States
// ---------------------------------------------------------------------------

// State represents the current state of the circuit breaker.
type State int

const (
	// StateClosed is normal operation. Requests pass through and failures
	// are counted. The breaker trips to Open when failures exceed the
	// configured threshold.
	StateClosed State = iota

	// StateOpen means the breaker has tripped. All requests are rejected
	// immediately with ErrBreakerOpen. After the cooldown period elapses,
	// the breaker moves to HalfOpen.
	StateOpen

	// StateHalfOpen is recovery testing. A limited number of probe
	// requests are allowed through. Successes close the breaker;
	// failures reopen it.
	StateHalfOpen
)

// String returns a human-readable name for the state.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "Closed"
	case StateOpen:
		return "Open"
	case StateHalfOpen:
		return "HalfOpen"
	default:
		return fmt.Sprintf("Unknown(%d)", int(s))
	}
}

// ---------------------------------------------------------------------------
// Intervention levels
// ---------------------------------------------------------------------------

// InterventionLevel represents the severity of the current escalation.
type InterventionLevel int

const (
	// InterventionNone means no intervention is active.
	InterventionNone InterventionLevel = iota

	// InterventionWarn means failures have reached the warning threshold.
	// The agent is still running but a human should be notified.
	InterventionWarn

	// InterventionPause means failures have reached the pause threshold.
	// The agent should be suspended until a human reviews and approves
	// resumption. This is the "human-in-the-loop" gate.
	InterventionPause

	// InterventionKill means the situation is deemed unrecoverable.
	// The agent process should be terminated. The breaker locks open
	// permanently until manually reset.
	InterventionKill
)

// String returns a human-readable name for the intervention level.
func (l InterventionLevel) String() string {
	switch l {
	case InterventionNone:
		return "None"
	case InterventionWarn:
		return "Warn"
	case InterventionPause:
		return "Pause"
	case InterventionKill:
		return "Kill"
	default:
		return fmt.Sprintf("Unknown(%d)", int(l))
	}
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

var (
	// ErrBreakerOpen is returned by ShouldAllow() and Execute() when the
	// circuit breaker is in the Open state and the cooldown has not elapsed.
	ErrBreakerOpen = errors.New("circuit breaker is open")

	// ErrBreakerKilled is returned when the breaker has been permanently
	// killed. The only recovery is a manual Reset().
	ErrBreakerKilled = errors.New("circuit breaker is killed (permanent)")

	// ErrBreakerPaused is returned when the breaker is paused awaiting
	// human-in-the-loop approval.
	ErrBreakerPaused = errors.New("circuit breaker is paused (awaiting human approval)")

	// ErrRetryBudgetExhausted is returned when the maximum number of
	// probe requests in the HalfOpen state has been used up without
	// achieving the required successes.
	ErrRetryBudgetExhausted = errors.New("retry budget exhausted in half-open state")
)

// ---------------------------------------------------------------------------
// Callbacks
// ---------------------------------------------------------------------------

// InterventionEvent is passed to callback functions when an intervention
// fires. It contains everything a handler needs to take action (send a
// notification, SIGSTOP a process, write an LTF trace, etc.).
type InterventionEvent struct {
	Level           InterventionLevel
	State           State
	FailureCount    int64
	SuccessCount    int64
	ConsecutiveFail int64
	TotalRequests   int64
	Timestamp       time.Time
	Reason          string
}

// OnInterventionFunc is called when the breaker escalates to a new
// intervention level. Implementations should be non-blocking; long-running
// work (e.g., sending notifications) should be dispatched asynchronously.
type OnInterventionFunc func(event InterventionEvent)

// OnStateChangeFunc is called when the breaker transitions between states
// (Closed/Open/HalfOpen). Modeled after sony/gobreaker's OnStateChange.
type OnStateChangeFunc func(from, to State)

// HumanApprovalFunc is called when the breaker enters the Pause
// intervention level. It should block until the human makes a decision:
// return true to resume, false to escalate to Kill.
//
// This is the "human-in-the-loop" gate. In a CLI tool like LoopGuard,
// this would prompt the user on stdin. In a web service, it might
// wait on a channel connected to an approval API endpoint.
//
// The function receives the intervention event for context.
type HumanApprovalFunc func(event InterventionEvent) bool

// ---------------------------------------------------------------------------
// Counts
// ---------------------------------------------------------------------------

// Counts tracks request outcomes. Modeled after sony/gobreaker's Counts
// struct but extended with fields relevant to AI agent supervision.
type Counts struct {
	Requests             int64 // total requests seen
	Successes            int64 // total successes
	Failures             int64 // total failures
	ConsecutiveSuccesses int64 // current consecutive success streak
	ConsecutiveFailures  int64 // current consecutive failure streak
	RetriesUsed          int64 // probe requests consumed in current HalfOpen window
}

// Reset zeros all counters.
func (c *Counts) Reset() {
	c.Requests = 0
	c.Successes = 0
	c.Failures = 0
	c.ConsecutiveSuccesses = 0
	c.ConsecutiveFailures = 0
	c.RetriesUsed = 0
}

// recordSuccess updates counters for a successful request.
func (c *Counts) recordSuccess() {
	c.Requests++
	c.Successes++
	c.ConsecutiveSuccesses++
	c.ConsecutiveFailures = 0
}

// recordFailure updates counters for a failed request.
func (c *Counts) recordFailure() {
	c.Requests++
	c.Failures++
	c.ConsecutiveFailures++
	c.ConsecutiveSuccesses = 0
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// Config holds all tunable parameters for the circuit breaker.
// Zero values for threshold fields disable that check.
type Config struct {
	// --- State transition thresholds ---

	// FailureThreshold is the number of consecutive failures in the
	// Closed state that trips the breaker to Open. Default: 5.
	// This mirrors sony/gobreaker's default ReadyToTrip behavior.
	FailureThreshold int64

	// SuccessThreshold is the number of consecutive successes needed
	// in the HalfOpen state to close the breaker. Default: 2.
	SuccessThreshold int64

	// --- Cooldown timers ---

	// OpenCooldown is how long the breaker stays Open before
	// transitioning to HalfOpen. Default: 30 seconds.
	// sony/gobreaker calls this "Timeout" and defaults to 60s.
	OpenCooldown time.Duration

	// CooldownMultiplier scales the OpenCooldown on each successive
	// trip (exponential backoff). For example, 2.0 means each trip
	// doubles the cooldown. Default: 1.5.
	CooldownMultiplier float64

	// MaxCooldown caps the exponentially-growing cooldown.
	// Default: 5 minutes.
	MaxCooldown time.Duration

	// --- Retry budget (HalfOpen state) ---

	// MaxRetries is the maximum number of probe requests allowed in
	// a single HalfOpen window. If all retries are consumed without
	// reaching SuccessThreshold, the breaker reopens and the retry
	// budget resets. Default: 3.
	// Modeled after Google SRE retry budget pattern and octo/retry.
	MaxRetries int64

	// --- Escalating intervention thresholds ---
	// Each threshold is based on cumulative (not consecutive) failures.

	// WarnThreshold: when total failures reach this count, the Warn
	// intervention fires. Default: 3.
	WarnThreshold int64

	// PauseThreshold: when total failures reach this count, the Pause
	// intervention fires and the breaker blocks for human approval
	// (if HumanApproval is set). Default: 7.
	PauseThreshold int64

	// KillThreshold: when total failures reach this count, the Kill
	// intervention fires and the breaker locks open permanently.
	// Default: 10.
	KillThreshold int64

	// --- Callbacks ---

	// OnIntervention is called when the intervention level escalates.
	// It is called with the mutex held; implementations must not
	// call back into the CircuitBreaker.
	OnIntervention OnInterventionFunc

	// OnStateChange is called when the breaker transitions between
	// Closed, Open, and HalfOpen states.
	OnStateChange OnStateChangeFunc

	// HumanApproval is called when the Pause intervention fires.
	// If nil, the pause intervention sets ErrBreakerPaused but does
	// not block. If non-nil, it is called outside the mutex to avoid
	// deadlock; the breaker remains paused until it returns.
	//
	// Return true to resume (breaker resets to Closed).
	// Return false to escalate to Kill.
	HumanApproval HumanApprovalFunc

	// --- Interval-based clearing (closed state) ---

	// ClearInterval is the cyclic period in the Closed state for
	// clearing internal counts. This allows the breaker to "forget"
	// old failures if the system has been healthy for a while.
	// If zero, counts are never cleared in the Closed state.
	// Modeled after sony/gobreaker's Interval field.
	ClearInterval time.Duration
}

// applyDefaults fills zero-valued fields with sensible defaults.
func (c *Config) applyDefaults() {
	if c.FailureThreshold <= 0 {
		c.FailureThreshold = 5
	}
	if c.SuccessThreshold <= 0 {
		c.SuccessThreshold = 2
	}
	if c.OpenCooldown <= 0 {
		c.OpenCooldown = 30 * time.Second
	}
	if c.CooldownMultiplier <= 0 {
		c.CooldownMultiplier = 1.5
	}
	if c.MaxCooldown <= 0 {
		c.MaxCooldown = 5 * time.Minute
	}
	if c.MaxRetries <= 0 {
		c.MaxRetries = 3
	}
	if c.WarnThreshold <= 0 {
		c.WarnThreshold = 3
	}
	if c.PauseThreshold <= 0 {
		c.PauseThreshold = 7
	}
	if c.KillThreshold <= 0 {
		c.KillThreshold = 10
	}
}

// ---------------------------------------------------------------------------
// CircuitBreaker
// ---------------------------------------------------------------------------

// CircuitBreaker is a state machine that prevents sending requests likely
// to fail, with escalating interventions for AI agent supervision.
//
// The design combines the classic Closed/Open/HalfOpen circuit breaker
// pattern (as popularized by Michael Nygard and implemented in
// sony/gobreaker) with a layered intervention system (Warn/Pause/Kill)
// tailored for AI agent loops.
//
// All exported methods are safe for concurrent use.
type CircuitBreaker struct {
	mu sync.Mutex

	// Configuration (immutable after construction).
	config Config

	// Current state.
	state State

	// Current intervention level.
	intervention InterventionLevel

	// Counters.
	counts Counts

	// Timing.
	lastStateChange time.Time     // when the current state was entered
	lastClearTime   time.Time     // when counts were last cleared in Closed state
	currentCooldown time.Duration // current cooldown (grows with multiplier)
	tripCount       int           // how many times the breaker has tripped

	// Kill lock: once killed, the breaker stays open until Reset().
	killed bool

	// Pause lock: when paused, block until human approval.
	paused  bool
	pauseCh chan struct{} // closed when pause is released
}

// New creates a CircuitBreaker with the given configuration.
// Zero-valued config fields are replaced with sensible defaults.
func New(cfg Config) *CircuitBreaker {
	cfg.applyDefaults()

	now := time.Now()
	return &CircuitBreaker{
		config:          cfg,
		state:           StateClosed,
		intervention:    InterventionNone,
		lastStateChange: now,
		lastClearTime:   now,
		currentCooldown: cfg.OpenCooldown,
		pauseCh:         make(chan struct{}),
	}
}

// ---------------------------------------------------------------------------
// Core API
// ---------------------------------------------------------------------------

// ShouldAllow checks whether a request should be permitted through the
// circuit breaker. This is the pre-execution guard: call it BEFORE each
// operation (API call, tool invocation, etc.).
//
// Returns nil if the request is allowed, or one of:
//   - ErrBreakerOpen: breaker is open, cooldown has not elapsed
//   - ErrBreakerKilled: breaker is permanently killed
//   - ErrBreakerPaused: breaker is paused awaiting human approval
//   - ErrRetryBudgetExhausted: HalfOpen retry budget is used up
//
// In the HalfOpen state, ShouldAllow also consumes one retry from the
// retry budget.
func (cb *CircuitBreaker) ShouldAllow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Permanent kill takes absolute precedence.
	if cb.killed {
		return ErrBreakerKilled
	}

	// Pause check.
	if cb.paused {
		return ErrBreakerPaused
	}

	now := time.Now()

	switch cb.state {
	case StateClosed:
		// Check if we should clear counts based on ClearInterval.
		if cb.config.ClearInterval > 0 && now.Sub(cb.lastClearTime) >= cb.config.ClearInterval {
			cb.counts.Reset()
			cb.lastClearTime = now
		}
		return nil

	case StateOpen:
		// Has the cooldown elapsed?
		if now.Sub(cb.lastStateChange) >= cb.currentCooldown {
			cb.transitionTo(StateHalfOpen, now)
			// Allow this request as the first probe.
			cb.counts.RetriesUsed = 1
			return nil
		}
		return ErrBreakerOpen

	case StateHalfOpen:
		// Check retry budget.
		if cb.counts.RetriesUsed >= cb.config.MaxRetries {
			return ErrRetryBudgetExhausted
		}
		cb.counts.RetriesUsed++
		return nil

	default:
		return ErrBreakerOpen
	}
}

// RecordSuccess records a successful request outcome. Call this after
// a request completes successfully.
//
// In the HalfOpen state, if consecutive successes reach the
// SuccessThreshold, the breaker closes and all counters reset.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.killed {
		return
	}

	cb.counts.recordSuccess()

	switch cb.state {
	case StateHalfOpen:
		if cb.counts.ConsecutiveSuccesses >= cb.config.SuccessThreshold {
			// Recovery confirmed. Close the breaker and reset.
			cb.transitionTo(StateClosed, time.Now())
			cb.counts.Reset()
			cb.currentCooldown = cb.config.OpenCooldown
			cb.tripCount = 0
			cb.intervention = InterventionNone
		}
	case StateClosed:
		// In Closed state, successes are good — no action needed
		// beyond updating the counters.
	}
}

// RecordFailure records a failed request outcome. Call this after a
// request fails.
//
// This method handles:
//   - Counting failures
//   - Tripping the breaker (Closed -> Open) when threshold is met
//   - Reopening the breaker (HalfOpen -> Open) on any failure
//   - Escalating interventions (Warn -> Pause -> Kill)
//   - Triggering human-in-the-loop pause when configured
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()

	if cb.killed {
		cb.mu.Unlock()
		return
	}

	cb.counts.recordFailure()

	now := time.Now()

	switch cb.state {
	case StateClosed:
		if cb.counts.ConsecutiveFailures >= cb.config.FailureThreshold {
			cb.trip(now)
		}
	case StateHalfOpen:
		// Any failure in HalfOpen reopens the breaker.
		cb.trip(now)
	}

	// Evaluate escalating interventions based on cumulative failures.
	cb.evaluateInterventions(now)

	// If we need to do a human-approval pause, we must release the
	// mutex first to avoid blocking all goroutines.
	needsHumanApproval := cb.paused && cb.config.HumanApproval != nil
	var approvalEvent InterventionEvent
	if needsHumanApproval {
		approvalEvent = cb.makeEvent(InterventionPause, "Cumulative failures reached pause threshold", now)
	}

	cb.mu.Unlock()

	// Human-in-the-loop gate (runs outside the mutex).
	if needsHumanApproval {
		approved := cb.config.HumanApproval(approvalEvent)
		cb.resolveHumanApproval(approved)
	}
}

// Execute wraps a function call with the circuit breaker. It calls
// ShouldAllow() before the function and records the outcome after.
//
// If the function returns nil, it is counted as a success.
// If the function returns a non-nil error, it is counted as a failure.
// If ShouldAllow() rejects the request, the function is never called.
//
// Modeled after sony/gobreaker's Execute method.
func (cb *CircuitBreaker) Execute(fn func() error) error {
	if err := cb.ShouldAllow(); err != nil {
		return err
	}
	err := fn()
	if err != nil {
		cb.RecordFailure()
		return err
	}
	cb.RecordSuccess()
	return nil
}

// CurrentState returns the breaker's current state.
func (cb *CircuitBreaker) CurrentState() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Check for implicit transition from Open -> HalfOpen.
	if cb.state == StateOpen && !cb.killed && time.Since(cb.lastStateChange) >= cb.currentCooldown {
		cb.transitionTo(StateHalfOpen, time.Now())
	}
	return cb.state
}

// CurrentIntervention returns the current intervention level.
func (cb *CircuitBreaker) CurrentIntervention() InterventionLevel {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.intervention
}

// GetCounts returns a snapshot of the current counters.
func (cb *CircuitBreaker) GetCounts() Counts {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.counts
}

// IsKilled returns true if the breaker has been permanently killed.
func (cb *CircuitBreaker) IsKilled() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.killed
}

// IsPaused returns true if the breaker is currently paused awaiting
// human approval.
func (cb *CircuitBreaker) IsPaused() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.paused
}

// TripCount returns how many times the breaker has tripped from
// Closed to Open.
func (cb *CircuitBreaker) TripCount() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.tripCount
}

// CurrentCooldown returns the current cooldown duration (may have
// been scaled up by the CooldownMultiplier on repeated trips).
func (cb *CircuitBreaker) CurrentCooldown() time.Duration {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.currentCooldown
}

// CooldownRemaining returns how much time is left before the breaker
// will transition from Open to HalfOpen. Returns 0 if not in Open state.
func (cb *CircuitBreaker) CooldownRemaining() time.Duration {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state != StateOpen {
		return 0
	}
	remaining := cb.currentCooldown - time.Since(cb.lastStateChange)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// Reset fully resets the circuit breaker to its initial Closed state.
// This clears the kill flag, pause state, all counters, the cooldown
// multiplier, and the trip count. Use this for manual recovery after
// a Kill intervention.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	oldState := cb.state
	cb.state = StateClosed
	cb.killed = false
	cb.paused = false
	cb.intervention = InterventionNone
	cb.counts.Reset()
	cb.currentCooldown = cb.config.OpenCooldown
	cb.tripCount = 0
	cb.lastStateChange = time.Now()
	cb.lastClearTime = time.Now()

	// Signal any goroutines waiting on pause.
	select {
	case <-cb.pauseCh:
		// Already closed.
	default:
		close(cb.pauseCh)
	}
	cb.pauseCh = make(chan struct{})

	if cb.config.OnStateChange != nil && oldState != StateClosed {
		cb.config.OnStateChange(oldState, StateClosed)
	}
}

// Resume releases a pause without going through the HumanApproval
// callback. This is useful for external resume commands (e.g.,
// "loopguard resume" CLI, or an API endpoint).
func (cb *CircuitBreaker) Resume() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if !cb.paused {
		return
	}

	cb.paused = false

	// Signal any goroutines waiting on pause.
	select {
	case <-cb.pauseCh:
	default:
		close(cb.pauseCh)
	}
	cb.pauseCh = make(chan struct{})

	// Transition back to closed and reset counts.
	cb.transitionTo(StateClosed, time.Now())
	cb.counts.Reset()
	cb.currentCooldown = cb.config.OpenCooldown
	cb.tripCount = 0
	cb.intervention = InterventionNone
}

// WaitForResume blocks until the breaker is no longer paused.
// Returns immediately if not paused. This allows agent loops to
// block cleanly at the supervision boundary rather than busy-waiting.
func (cb *CircuitBreaker) WaitForResume() {
	cb.mu.Lock()
	if !cb.paused {
		cb.mu.Unlock()
		return
	}
	ch := cb.pauseCh
	cb.mu.Unlock()

	// Block until the channel is closed (Resume or Reset called).
	<-ch
}

// ---------------------------------------------------------------------------
// Snapshot for observability
// ---------------------------------------------------------------------------

// Snapshot is a point-in-time view of the circuit breaker's full state.
// Useful for dashboards, logging, and LTF trace emission.
type Snapshot struct {
	Timestamp         time.Time
	State             State
	Intervention      InterventionLevel
	Counts            Counts
	Killed            bool
	Paused            bool
	TripCount         int
	CurrentCooldown   time.Duration
	CooldownRemaining time.Duration
	LastStateChange   time.Time
}

// TakeSnapshot returns a consistent point-in-time view of all state.
func (cb *CircuitBreaker) TakeSnapshot() Snapshot {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	var remaining time.Duration
	if cb.state == StateOpen {
		remaining = cb.currentCooldown - time.Since(cb.lastStateChange)
		if remaining < 0 {
			remaining = 0
		}
	}

	return Snapshot{
		Timestamp:         time.Now(),
		State:             cb.state,
		Intervention:      cb.intervention,
		Counts:            cb.counts,
		Killed:            cb.killed,
		Paused:            cb.paused,
		TripCount:         cb.tripCount,
		CurrentCooldown:   cb.currentCooldown,
		CooldownRemaining: remaining,
		LastStateChange:   cb.lastStateChange,
	}
}

// Summary returns a human-readable status string.
func (cb *CircuitBreaker) Summary() string {
	s := cb.TakeSnapshot()
	return fmt.Sprintf(
		"CircuitBreaker Status\n"+
			"  State:              %s\n"+
			"  Intervention:       %s\n"+
			"  Total requests:     %d\n"+
			"  Successes:          %d\n"+
			"  Failures:           %d\n"+
			"  Consecutive fails:  %d\n"+
			"  Trip count:         %d\n"+
			"  Current cooldown:   %s\n"+
			"  Cooldown remaining: %s\n"+
			"  Killed:             %v\n"+
			"  Paused:             %v",
		s.State,
		s.Intervention,
		s.Counts.Requests,
		s.Counts.Successes,
		s.Counts.Failures,
		s.Counts.ConsecutiveFailures,
		s.TripCount,
		s.CurrentCooldown,
		s.CooldownRemaining,
		s.Killed,
		s.Paused,
	)
}

// ---------------------------------------------------------------------------
// Internal helpers (must be called with cb.mu held)
// ---------------------------------------------------------------------------

// transitionTo changes the state and fires the OnStateChange callback.
func (cb *CircuitBreaker) transitionTo(newState State, now time.Time) {
	oldState := cb.state
	if oldState == newState {
		return
	}
	cb.state = newState
	cb.lastStateChange = now

	// Reset retry budget on entering HalfOpen.
	if newState == StateHalfOpen {
		cb.counts.RetriesUsed = 0
		cb.counts.ConsecutiveSuccesses = 0
		cb.counts.ConsecutiveFailures = 0
	}

	if cb.config.OnStateChange != nil {
		cb.config.OnStateChange(oldState, newState)
	}
}

// trip moves the breaker from Closed/HalfOpen to Open, scaling the
// cooldown with the exponential backoff multiplier.
func (cb *CircuitBreaker) trip(now time.Time) {
	cb.tripCount++

	// Exponential backoff on cooldown: each trip multiplies the cooldown
	// by CooldownMultiplier, capped at MaxCooldown.
	if cb.tripCount > 1 {
		newCooldown := time.Duration(float64(cb.currentCooldown) * cb.config.CooldownMultiplier)
		if newCooldown > cb.config.MaxCooldown {
			newCooldown = cb.config.MaxCooldown
		}
		cb.currentCooldown = newCooldown
	}

	cb.transitionTo(StateOpen, now)
}

// evaluateInterventions checks cumulative failure count against the
// escalation thresholds and fires callbacks as needed.
func (cb *CircuitBreaker) evaluateInterventions(now time.Time) {
	totalFailures := cb.counts.Failures

	// Kill threshold (highest priority).
	if cb.config.KillThreshold > 0 && totalFailures >= cb.config.KillThreshold && cb.intervention != InterventionKill {
		cb.intervention = InterventionKill
		cb.killed = true
		cb.state = StateOpen
		if cb.config.OnIntervention != nil {
			cb.config.OnIntervention(cb.makeEvent(
				InterventionKill,
				fmt.Sprintf("Cumulative failures (%d) reached kill threshold (%d)", totalFailures, cb.config.KillThreshold),
				now,
			))
		}
		return
	}

	// Pause threshold.
	if cb.config.PauseThreshold > 0 && totalFailures >= cb.config.PauseThreshold && cb.intervention < InterventionPause {
		cb.intervention = InterventionPause
		cb.paused = true

		// Create a new pause channel for WaitForResume.
		select {
		case <-cb.pauseCh:
			// Already closed from a previous cycle; make a new one.
			cb.pauseCh = make(chan struct{})
		default:
			// Channel is still open, which is what we want.
		}

		if cb.config.OnIntervention != nil {
			cb.config.OnIntervention(cb.makeEvent(
				InterventionPause,
				fmt.Sprintf("Cumulative failures (%d) reached pause threshold (%d)", totalFailures, cb.config.PauseThreshold),
				now,
			))
		}
		return
	}

	// Warn threshold.
	if cb.config.WarnThreshold > 0 && totalFailures >= cb.config.WarnThreshold && cb.intervention < InterventionWarn {
		cb.intervention = InterventionWarn
		if cb.config.OnIntervention != nil {
			cb.config.OnIntervention(cb.makeEvent(
				InterventionWarn,
				fmt.Sprintf("Cumulative failures (%d) reached warn threshold (%d)", totalFailures, cb.config.WarnThreshold),
				now,
			))
		}
	}
}

// makeEvent constructs an InterventionEvent from current state.
func (cb *CircuitBreaker) makeEvent(level InterventionLevel, reason string, now time.Time) InterventionEvent {
	return InterventionEvent{
		Level:           level,
		State:           cb.state,
		FailureCount:    cb.counts.Failures,
		SuccessCount:    cb.counts.Successes,
		ConsecutiveFail: cb.counts.ConsecutiveFailures,
		TotalRequests:   cb.counts.Requests,
		Timestamp:       now,
		Reason:          reason,
	}
}

// resolveHumanApproval is called outside the mutex after the
// HumanApproval callback returns.
func (cb *CircuitBreaker) resolveHumanApproval(approved bool) {
	if approved {
		cb.Resume()
	} else {
		// Escalate to Kill.
		cb.mu.Lock()
		cb.intervention = InterventionKill
		cb.killed = true
		cb.paused = false
		cb.state = StateOpen

		// Signal any goroutines waiting on pause.
		select {
		case <-cb.pauseCh:
		default:
			close(cb.pauseCh)
		}
		cb.pauseCh = make(chan struct{})

		if cb.config.OnIntervention != nil {
			cb.config.OnIntervention(cb.makeEvent(
				InterventionKill,
				"Human rejected resumption — escalating to Kill",
				time.Now(),
			))
		}
		cb.mu.Unlock()
	}
}
