// Package tokenburn tracks cumulative token usage, calculates burn rate
// via exponential moving average, estimates cost in dollars, and enforces
// budget limits for AI agent loops.
//
// Design informed by:
//   - VividCortex/ewma EMA algorithm (alpha = 2/(N+1))
//   - rcrowley/go-metrics 1m/5m/15m rate averages
//   - mxk/go-flowrate weight = 1 - exp(-dt/window)
//   - Claude API usage object: input_tokens, output_tokens, cache_read_input_tokens, cache_creation_input_tokens
//   - OpenAI API usage object: prompt_tokens, completion_tokens, total_tokens
//   - Circuit-breaker pattern for agent loop detection (>4K tokens/min sustained = likely loop)
//   - Pre-execution budget guard pattern (check before each API call, not after)
package tokenburn

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Model pricing
// ---------------------------------------------------------------------------

// ModelPricing holds per-token cost in USD for a specific model.
type ModelPricing struct {
	InputPerMTok  float64 // USD per 1 million input tokens
	OutputPerMTok float64 // USD per 1 million output tokens
}

// InputCost returns the cost of n input tokens.
func (p ModelPricing) InputCost(n int64) float64 {
	return float64(n) * p.InputPerMTok / 1_000_000
}

// OutputCost returns the cost of n output tokens.
func (p ModelPricing) OutputCost(n int64) float64 {
	return float64(n) * p.OutputPerMTok / 1_000_000
}

// Pre-configured pricing for common models (mid-2026 rates).
// Sources:
//   - Claude: https://platform.claude.com/docs/en/about-claude/pricing
//   - OpenAI: https://developers.openai.com/api/docs/pricing
var (
	// Anthropic Claude models
	PricingClaudeOpus48   = ModelPricing{InputPerMTok: 5.00, OutputPerMTok: 25.00}
	PricingClaudeOpus47   = ModelPricing{InputPerMTok: 5.00, OutputPerMTok: 25.00}
	PricingClaudeSonnet46 = ModelPricing{InputPerMTok: 3.00, OutputPerMTok: 15.00}
	PricingClaudeHaiku45  = ModelPricing{InputPerMTok: 1.00, OutputPerMTok: 5.00}

	// OpenAI GPT models
	PricingGPT4o     = ModelPricing{InputPerMTok: 2.50, OutputPerMTok: 10.00}
	PricingGPT4oMini = ModelPricing{InputPerMTok: 0.15, OutputPerMTok: 0.60}
	PricingGPT41     = ModelPricing{InputPerMTok: 2.00, OutputPerMTok: 8.00}
	PricingGPT41Mini = ModelPricing{InputPerMTok: 0.40, OutputPerMTok: 1.60}
	PricingGPT41Nano = ModelPricing{InputPerMTok: 0.10, OutputPerMTok: 0.40}
	PricingO3        = ModelPricing{InputPerMTok: 2.00, OutputPerMTok: 8.00}
	PricingO4Mini    = ModelPricing{InputPerMTok: 0.55, OutputPerMTok: 2.20}
	PricingGPT55     = ModelPricing{InputPerMTok: 5.00, OutputPerMTok: 30.00}
	PricingGPT54     = ModelPricing{InputPerMTok: 2.50, OutputPerMTok: 15.00}
)

// ---------------------------------------------------------------------------
// Token event
// ---------------------------------------------------------------------------

// TokenEvent represents a single API call's token usage, captured from the
// provider's response usage object.
//
// Claude API returns:
//
//	{"usage": {"input_tokens": N, "output_tokens": N,
//	           "cache_creation_input_tokens": N, "cache_read_input_tokens": N}}
//
// OpenAI API returns:
//
//	{"usage": {"prompt_tokens": N, "completion_tokens": N, "total_tokens": N}}
type TokenEvent struct {
	Timestamp    time.Time
	InputTokens  int64
	OutputTokens int64
	Model        string // optional: for multi-model cost tracking
}

// Total returns input + output tokens.
func (e TokenEvent) Total() int64 {
	return e.InputTokens + e.OutputTokens
}

// ---------------------------------------------------------------------------
// Budget config
// ---------------------------------------------------------------------------

// BudgetConfig defines spending limits.
type BudgetConfig struct {
	// WarnThresholdUSD triggers a warning when cumulative cost reaches this.
	// Typical starting point: 80% of HardLimitUSD.
	WarnThresholdUSD float64

	// HardLimitUSD is the absolute ceiling. IsOverBudget() returns true above this.
	// Recommended starting points from research:
	//   - Small team tools: $25/day
	//   - Production SaaS agents: $100/day
	HardLimitUSD float64

	// MaxTokensPerMinute triggers circuit-breaker behavior when burn rate exceeds this.
	// Research suggests >4000 tokens/min sustained indicates a likely infinite loop,
	// since healthy agents rarely sustain more than 4K tok/min due to I/O wait.
	MaxTokensPerMinute float64
}

// BudgetStatus is returned by CheckBudget().
type BudgetStatus int

const (
	BudgetOK BudgetStatus = iota
	BudgetWarning
	BudgetExceeded
	BudgetRateExceeded // circuit breaker: burn rate too high
)

func (s BudgetStatus) String() string {
	switch s {
	case BudgetOK:
		return "OK"
	case BudgetWarning:
		return "WARNING"
	case BudgetExceeded:
		return "EXCEEDED"
	case BudgetRateExceeded:
		return "RATE_EXCEEDED"
	default:
		return "UNKNOWN"
	}
}

// ---------------------------------------------------------------------------
// EMA (exponential moving average for rate smoothing)
// ---------------------------------------------------------------------------

// ema computes an exponentially weighted moving average.
// Algorithm from VividCortex/ewma: alpha = 2/(age+1), where age is the
// average age of samples in seconds. For a 1-minute window, age=30,
// giving alpha ~= 0.0645.
//
// For time-weighted rate calculation we use the go-flowrate approach:
//
//	weight = 1 - exp(-dt/windowSize)
//	newRate = weight*sampleRate + (1-weight)*oldRate
//
// This naturally handles irregular sample intervals.
type ema struct {
	value      float64
	window     time.Duration // smoothing window
	lastUpdate time.Time
	ready      bool
}

func newEMA(window time.Duration) *ema {
	return &ema{window: window}
}

// update adds a new rate observation at the given timestamp. The rate should
// be in tokens/minute (or whatever unit you want the output in).
func (e *ema) update(rate float64, now time.Time) {
	if !e.ready {
		e.value = rate
		e.lastUpdate = now
		e.ready = true
		return
	}
	dt := now.Sub(e.lastUpdate).Seconds()
	if dt <= 0 {
		return
	}
	windowSec := e.window.Seconds()
	if windowSec <= 0 {
		windowSec = 60 // fallback: 1 minute
	}
	// weight formula from mxk/go-flowrate
	weight := 1 - math.Exp(-dt/windowSec)
	e.value = weight*rate + (1-weight)*e.value
	e.lastUpdate = now
}

// Value returns the current smoothed rate.
func (e *ema) Value() float64 {
	if !e.ready {
		return 0
	}
	return e.value
}

// ---------------------------------------------------------------------------
// TokenBurnTracker
// ---------------------------------------------------------------------------

const defaultSlidingWindowSize = 100

// TokenBurnTracker is the core component for monitoring token consumption
// in AI agent loops. It implements the pre-execution budget guard pattern:
// call CheckBudget() BEFORE each API call to enforce limits proactively,
// not reactively.
//
// Thread-safe for concurrent use.
type TokenBurnTracker struct {
	mu sync.RWMutex

	// Cumulative counters
	totalInputTokens  int64
	totalOutputTokens int64
	totalCostUSD      float64
	eventCount        int64

	// Pricing
	pricing ModelPricing

	// Budget enforcement
	budget BudgetConfig

	// Rate calculation via EMA
	rateEMA *ema // smoothed tokens/minute

	// Sliding window of recent events for granular rate analysis.
	// Keeps the last N events; older events are evicted.
	recentEvents []TokenEvent
	windowSize   int

	// Timing
	startTime time.Time
	lastEvent time.Time
}

// Option configures a TokenBurnTracker.
type Option func(*TokenBurnTracker)

// WithPricing sets the model pricing.
func WithPricing(p ModelPricing) Option {
	return func(t *TokenBurnTracker) { t.pricing = p }
}

// WithBudget sets budget limits.
func WithBudget(b BudgetConfig) Option {
	return func(t *TokenBurnTracker) { t.budget = b }
}

// WithEMAWindow sets the EMA smoothing window duration.
// Default is 1 minute.
func WithEMAWindow(d time.Duration) Option {
	return func(t *TokenBurnTracker) { t.rateEMA = newEMA(d) }
}

// WithSlidingWindowSize sets how many recent events to retain.
// Default is 100.
func WithSlidingWindowSize(n int) Option {
	return func(t *TokenBurnTracker) {
		t.windowSize = n
		t.recentEvents = make([]TokenEvent, 0, n)
	}
}

// New creates a TokenBurnTracker with the given options.
func New(opts ...Option) *TokenBurnTracker {
	t := &TokenBurnTracker{
		rateEMA:      newEMA(1 * time.Minute),
		windowSize:   defaultSlidingWindowSize,
		recentEvents: make([]TokenEvent, 0, defaultSlidingWindowSize),
		startTime:    time.Now(),
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// RecordEvent logs a token usage event and updates all internal state.
// This should be called after each API response is received, using data
// from the provider's usage object.
//
// For Claude, extract from response.usage:
//
//	RecordEvent(TokenEvent{
//	    Timestamp:    time.Now(),
//	    InputTokens:  response.Usage.InputTokens,
//	    OutputTokens: response.Usage.OutputTokens,
//	})
//
// For OpenAI, extract from response.usage:
//
//	RecordEvent(TokenEvent{
//	    Timestamp:    time.Now(),
//	    InputTokens:  response.Usage.PromptTokens,
//	    OutputTokens: response.Usage.CompletionTokens,
//	})
func (t *TokenBurnTracker) RecordEvent(e TokenEvent) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Update cumulative counters
	t.totalInputTokens += e.InputTokens
	t.totalOutputTokens += e.OutputTokens
	t.eventCount++

	// Update cumulative cost
	t.totalCostUSD += t.pricing.InputCost(e.InputTokens) +
		t.pricing.OutputCost(e.OutputTokens)

	// Compute instantaneous rate (tokens/minute) for this interval
	var instantRate float64
	if !t.lastEvent.IsZero() {
		dt := e.Timestamp.Sub(t.lastEvent)
		if dt > 0 {
			// tokens in this event / fraction of a minute
			instantRate = float64(e.Total()) / dt.Minutes()
		}
	} else {
		// First event: use total tokens as the initial rate observation
		instantRate = float64(e.Total())
	}

	// Update EMA with the instantaneous rate
	t.rateEMA.update(instantRate, e.Timestamp)

	t.lastEvent = e.Timestamp

	// Append to sliding window, evicting oldest if full
	if len(t.recentEvents) >= t.windowSize {
		copy(t.recentEvents, t.recentEvents[1:])
		t.recentEvents = t.recentEvents[:t.windowSize-1]
	}
	t.recentEvents = append(t.recentEvents, e)
}

// ---------------------------------------------------------------------------
// Query methods
// ---------------------------------------------------------------------------

// BurnRate returns the current EMA-smoothed token consumption rate in
// tokens per minute.
func (t *TokenBurnTracker) BurnRate() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.rateEMA.Value()
}

// RawBurnRate returns the simple (non-smoothed) average tokens/minute
// calculated from total tokens divided by elapsed wall time.
func (t *TokenBurnTracker) RawBurnRate() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	elapsed := time.Since(t.startTime).Minutes()
	if elapsed <= 0 {
		return 0
	}
	return float64(t.totalInputTokens+t.totalOutputTokens) / elapsed
}

// WindowBurnRate calculates tokens/minute from the sliding window of
// recent events only (ignoring EMA smoothing). Useful for detecting
// recent spikes that the EMA may dampen.
func (t *TokenBurnTracker) WindowBurnRate() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if len(t.recentEvents) < 2 {
		return 0
	}
	first := t.recentEvents[0]
	last := t.recentEvents[len(t.recentEvents)-1]
	dt := last.Timestamp.Sub(first.Timestamp).Minutes()
	if dt <= 0 {
		return 0
	}
	var total int64
	for _, e := range t.recentEvents {
		total += e.Total()
	}
	return float64(total) / dt
}

// CumulativeTokens returns total input, output, and combined token counts.
func (t *TokenBurnTracker) CumulativeTokens() (input, output, total int64) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.totalInputTokens, t.totalOutputTokens,
		t.totalInputTokens + t.totalOutputTokens
}

// CumulativeCost returns the total estimated cost in USD.
func (t *TokenBurnTracker) CumulativeCost() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.totalCostUSD
}

// ProjectedCost estimates the total cost if the current burn rate continues
// for the given duration. Uses EMA-smoothed rate for the projection.
//
// Formula: currentCost + (burnRate * duration_in_minutes * avgCostPerToken)
func (t *TokenBurnTracker) ProjectedCost(duration time.Duration) float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	currentCost := t.totalCostUSD
	rate := t.rateEMA.Value() // tokens/minute
	if rate <= 0 || t.eventCount == 0 {
		return currentCost
	}

	// Average cost per token from observed data
	totalTokens := t.totalInputTokens + t.totalOutputTokens
	if totalTokens == 0 {
		return currentCost
	}
	avgCostPerToken := t.totalCostUSD / float64(totalTokens)

	projectedTokens := rate * duration.Minutes()
	return currentCost + (projectedTokens * avgCostPerToken)
}

// IsOverBudget returns true if cumulative cost exceeds the hard limit.
// This is the simplest budget check. For richer status use CheckBudget().
func (t *TokenBurnTracker) IsOverBudget() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.budget.HardLimitUSD <= 0 {
		return false // no limit configured
	}
	return t.totalCostUSD >= t.budget.HardLimitUSD
}

// CheckBudget performs a comprehensive pre-execution budget check.
// Call this BEFORE each API call to enforce limits proactively.
//
// Returns:
//   - BudgetRateExceeded if burn rate exceeds MaxTokensPerMinute (circuit breaker)
//   - BudgetExceeded if cost >= HardLimitUSD
//   - BudgetWarning if cost >= WarnThresholdUSD
//   - BudgetOK otherwise
func (t *TokenBurnTracker) CheckBudget() BudgetStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Circuit breaker: detect runaway loops via rate
	if t.budget.MaxTokensPerMinute > 0 && t.rateEMA.Value() > t.budget.MaxTokensPerMinute {
		return BudgetRateExceeded
	}

	if t.budget.HardLimitUSD > 0 && t.totalCostUSD >= t.budget.HardLimitUSD {
		return BudgetExceeded
	}

	if t.budget.WarnThresholdUSD > 0 && t.totalCostUSD >= t.budget.WarnThresholdUSD {
		return BudgetWarning
	}

	return BudgetOK
}

// EventCount returns the number of token events recorded.
func (t *TokenBurnTracker) EventCount() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.eventCount
}

// Elapsed returns how long the tracker has been active.
func (t *TokenBurnTracker) Elapsed() time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return time.Since(t.startTime)
}

// RecentEvents returns a copy of the sliding window of recent events.
func (t *TokenBurnTracker) RecentEvents() []TokenEvent {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]TokenEvent, len(t.recentEvents))
	copy(out, t.recentEvents)
	return out
}

// Snapshot returns a point-in-time summary of all tracker state.
type Snapshot struct {
	Timestamp         time.Time
	TotalInputTokens  int64
	TotalOutputTokens int64
	TotalTokens       int64
	TotalCostUSD      float64
	BurnRateTPM       float64 // EMA-smoothed tokens/minute
	RawBurnRateTPM    float64 // simple average tokens/minute
	WindowBurnRateTPM float64 // sliding window tokens/minute
	EventCount        int64
	Elapsed           time.Duration
	BudgetStatus      BudgetStatus
	ProjectedCost1h   float64
}

// TakeSnapshot returns a consistent point-in-time view of all metrics.
func (t *TokenBurnTracker) TakeSnapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()

	totalTok := t.totalInputTokens + t.totalOutputTokens
	elapsed := time.Since(t.startTime)

	var rawRate float64
	if mins := elapsed.Minutes(); mins > 0 {
		rawRate = float64(totalTok) / mins
	}

	var winRate float64
	if len(t.recentEvents) >= 2 {
		first := t.recentEvents[0]
		last := t.recentEvents[len(t.recentEvents)-1]
		if dt := last.Timestamp.Sub(first.Timestamp).Minutes(); dt > 0 {
			var winTotal int64
			for _, e := range t.recentEvents {
				winTotal += e.Total()
			}
			winRate = float64(winTotal) / dt
		}
	}

	// Projected cost for 1 hour
	projected := t.totalCostUSD
	rate := t.rateEMA.Value()
	if rate > 0 && totalTok > 0 {
		avgCostPerToken := t.totalCostUSD / float64(totalTok)
		projected += rate * 60 * avgCostPerToken
	}

	// Budget status (inline to avoid double-lock)
	status := BudgetOK
	if t.budget.MaxTokensPerMinute > 0 && rate > t.budget.MaxTokensPerMinute {
		status = BudgetRateExceeded
	} else if t.budget.HardLimitUSD > 0 && t.totalCostUSD >= t.budget.HardLimitUSD {
		status = BudgetExceeded
	} else if t.budget.WarnThresholdUSD > 0 && t.totalCostUSD >= t.budget.WarnThresholdUSD {
		status = BudgetWarning
	}

	return Snapshot{
		Timestamp:         time.Now(),
		TotalInputTokens:  t.totalInputTokens,
		TotalOutputTokens: t.totalOutputTokens,
		TotalTokens:       totalTok,
		TotalCostUSD:      t.totalCostUSD,
		BurnRateTPM:       rate,
		RawBurnRateTPM:    rawRate,
		WindowBurnRateTPM: winRate,
		EventCount:        t.eventCount,
		Elapsed:           elapsed,
		BudgetStatus:      status,
		ProjectedCost1h:   projected,
	}
}

// Summary returns a human-readable multi-line status string.
func (t *TokenBurnTracker) Summary() string {
	s := t.TakeSnapshot()
	return fmt.Sprintf(
		"Token Burn Report\n"+
			"  Elapsed:        %s\n"+
			"  Events:         %d\n"+
			"  Input tokens:   %d\n"+
			"  Output tokens:  %d\n"+
			"  Total tokens:   %d\n"+
			"  Cost:           $%.4f\n"+
			"  Burn rate (EMA): %.0f tok/min\n"+
			"  Burn rate (raw): %.0f tok/min\n"+
			"  Burn rate (win): %.0f tok/min\n"+
			"  Projected 1h:   $%.4f\n"+
			"  Budget status:  %s",
		s.Elapsed.Round(time.Second),
		s.EventCount,
		s.TotalInputTokens,
		s.TotalOutputTokens,
		s.TotalTokens,
		s.TotalCostUSD,
		s.BurnRateTPM,
		s.RawBurnRateTPM,
		s.WindowBurnRateTPM,
		s.ProjectedCost1h,
		s.BudgetStatus,
	)
}
