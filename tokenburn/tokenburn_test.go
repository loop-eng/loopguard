package tokenburn

import (
	"testing"
	"time"
)

func TestNewTracker(t *testing.T) {
	tracker := New(
		WithPricing(PricingClaudeSonnet46),
		WithBudget(BudgetConfig{
			WarnThresholdUSD:   0.80,
			HardLimitUSD:       1.00,
			MaxTokensPerMinute: 5000,
		}),
	)

	if tracker.BurnRate() != 0 {
		t.Error("initial burn rate should be 0")
	}
	if tracker.CumulativeCost() != 0 {
		t.Error("initial cost should be 0")
	}
	if tracker.IsOverBudget() {
		t.Error("should not be over budget initially")
	}
	if tracker.CheckBudget() != BudgetOK {
		t.Error("budget status should be OK initially")
	}
}

func TestCumulativeTokens(t *testing.T) {
	tracker := New(WithPricing(PricingClaudeSonnet46))

	now := time.Now()
	tracker.RecordEvent(TokenEvent{
		Timestamp:    now,
		InputTokens:  100,
		OutputTokens: 50,
	})
	tracker.RecordEvent(TokenEvent{
		Timestamp:    now.Add(10 * time.Second),
		InputTokens:  200,
		OutputTokens: 100,
	})

	input, output, total := tracker.CumulativeTokens()
	if input != 300 {
		t.Errorf("expected 300 input tokens, got %d", input)
	}
	if output != 150 {
		t.Errorf("expected 150 output tokens, got %d", output)
	}
	if total != 450 {
		t.Errorf("expected 450 total tokens, got %d", total)
	}
}

func TestCostCalculation(t *testing.T) {
	// Claude Sonnet 4.6: $3/MTok input, $15/MTok output
	tracker := New(WithPricing(PricingClaudeSonnet46))

	tracker.RecordEvent(TokenEvent{
		Timestamp:    time.Now(),
		InputTokens:  1_000_000, // 1M input tokens = $3.00
		OutputTokens: 100_000,   // 100K output tokens = $1.50
	})

	cost := tracker.CumulativeCost()
	expected := 3.00 + 1.50 // $4.50
	if diff := cost - expected; diff > 0.001 || diff < -0.001 {
		t.Errorf("expected cost $%.4f, got $%.4f", expected, cost)
	}
}

func TestCostCalculationOpenAI(t *testing.T) {
	// GPT-4.1: $2/MTok input, $8/MTok output
	tracker := New(WithPricing(PricingGPT41))

	tracker.RecordEvent(TokenEvent{
		Timestamp:    time.Now(),
		InputTokens:  500_000,
		OutputTokens: 200_000,
	})

	cost := tracker.CumulativeCost()
	// 0.5M * $2/M = $1.00 input
	// 0.2M * $8/M = $1.60 output
	expected := 1.00 + 1.60
	if diff := cost - expected; diff > 0.001 || diff < -0.001 {
		t.Errorf("expected cost $%.4f, got $%.4f", expected, cost)
	}
}

func TestBurnRateEMA(t *testing.T) {
	tracker := New(
		WithPricing(PricingClaudeSonnet46),
		WithEMAWindow(30*time.Second),
	)

	now := time.Now()

	// Simulate 10 events, 5 seconds apart, each with 500 tokens
	// That's 500 tokens per 5 seconds = 6000 tokens/minute
	for i := 0; i < 10; i++ {
		tracker.RecordEvent(TokenEvent{
			Timestamp:    now.Add(time.Duration(i) * 5 * time.Second),
			InputTokens:  300,
			OutputTokens: 200,
		})
	}

	rate := tracker.BurnRate()
	// Should converge toward ~6000 tok/min
	if rate < 3000 || rate > 9000 {
		t.Errorf("burn rate %.0f tok/min outside expected range [3000, 9000]", rate)
	}
}

func TestBudgetWarning(t *testing.T) {
	tracker := New(
		WithPricing(PricingClaudeHaiku45), // $1/$5 per MTok
		WithBudget(BudgetConfig{
			WarnThresholdUSD: 0.005, // $0.005
			HardLimitUSD:     0.010, // $0.01
		}),
	)

	// 5000 input tokens at $1/MTok = $0.005 -> hits warn threshold
	tracker.RecordEvent(TokenEvent{
		Timestamp:    time.Now(),
		InputTokens:  5000,
		OutputTokens: 0,
	})

	status := tracker.CheckBudget()
	if status != BudgetWarning {
		t.Errorf("expected BudgetWarning, got %s", status)
	}
}

func TestBudgetExceeded(t *testing.T) {
	tracker := New(
		WithPricing(PricingClaudeHaiku45),
		WithBudget(BudgetConfig{
			WarnThresholdUSD: 0.005,
			HardLimitUSD:     0.010,
		}),
	)

	// 10000 input tokens at $1/MTok = $0.01 -> hits hard limit
	tracker.RecordEvent(TokenEvent{
		Timestamp:    time.Now(),
		InputTokens:  10000,
		OutputTokens: 0,
	})

	if !tracker.IsOverBudget() {
		t.Error("expected IsOverBudget() to return true")
	}
	status := tracker.CheckBudget()
	if status != BudgetExceeded {
		t.Errorf("expected BudgetExceeded, got %s", status)
	}
}

func TestBudgetRateExceeded(t *testing.T) {
	tracker := New(
		WithPricing(PricingClaudeSonnet46),
		WithBudget(BudgetConfig{
			HardLimitUSD:       100.0, // high, won't trigger
			MaxTokensPerMinute: 1000,  // low, will trigger
		}),
	)

	now := time.Now()
	// Two events 1 second apart with 5000 tokens each = 300K tok/min
	tracker.RecordEvent(TokenEvent{
		Timestamp:    now,
		InputTokens:  2500,
		OutputTokens: 2500,
	})
	tracker.RecordEvent(TokenEvent{
		Timestamp:    now.Add(1 * time.Second),
		InputTokens:  2500,
		OutputTokens: 2500,
	})

	status := tracker.CheckBudget()
	if status != BudgetRateExceeded {
		t.Errorf("expected BudgetRateExceeded, got %s (rate=%.0f)", status, tracker.BurnRate())
	}
}

func TestProjectedCost(t *testing.T) {
	tracker := New(WithPricing(PricingClaudeSonnet46))

	now := time.Now()
	// Record events to establish a burn rate
	for i := 0; i < 5; i++ {
		tracker.RecordEvent(TokenEvent{
			Timestamp:    now.Add(time.Duration(i) * 10 * time.Second),
			InputTokens:  1000,
			OutputTokens: 500,
		})
	}

	projected := tracker.ProjectedCost(1 * time.Hour)
	currentCost := tracker.CumulativeCost()

	if projected <= currentCost {
		t.Errorf("projected cost ($%.4f) should exceed current cost ($%.4f)",
			projected, currentCost)
	}
}

func TestSlidingWindow(t *testing.T) {
	tracker := New(
		WithPricing(PricingClaudeSonnet46),
		WithSlidingWindowSize(5),
	)

	now := time.Now()
	// Record 10 events; window should only keep the last 5
	for i := 0; i < 10; i++ {
		tracker.RecordEvent(TokenEvent{
			Timestamp:    now.Add(time.Duration(i) * time.Second),
			InputTokens:  int64(i * 100),
			OutputTokens: 50,
		})
	}

	events := tracker.RecentEvents()
	if len(events) != 5 {
		t.Errorf("expected 5 events in window, got %d", len(events))
	}

	// First event in window should be event #5 (0-indexed)
	if events[0].InputTokens != 500 {
		t.Errorf("expected first windowed event to have 500 input tokens, got %d",
			events[0].InputTokens)
	}
}

func TestNoBudgetConfigured(t *testing.T) {
	// No budget means everything is OK
	tracker := New(WithPricing(PricingClaudeSonnet46))

	tracker.RecordEvent(TokenEvent{
		Timestamp:    time.Now(),
		InputTokens:  1_000_000,
		OutputTokens: 1_000_000,
	})

	if tracker.IsOverBudget() {
		t.Error("should not be over budget when no limit is set")
	}
	if tracker.CheckBudget() != BudgetOK {
		t.Error("budget should be OK when no limit is set")
	}
}

func TestEventCount(t *testing.T) {
	tracker := New()

	for i := 0; i < 42; i++ {
		tracker.RecordEvent(TokenEvent{
			InputTokens:  10,
			OutputTokens: 5,
		})
	}

	if tracker.EventCount() != 42 {
		t.Errorf("expected 42 events, got %d", tracker.EventCount())
	}
}

func TestSnapshot(t *testing.T) {
	tracker := New(
		WithPricing(PricingClaudeOpus48),
		WithBudget(BudgetConfig{
			WarnThresholdUSD: 1.00,
			HardLimitUSD:     5.00,
		}),
	)

	now := time.Now()
	tracker.RecordEvent(TokenEvent{
		Timestamp:    now,
		InputTokens:  10000,
		OutputTokens: 5000,
	})
	tracker.RecordEvent(TokenEvent{
		Timestamp:    now.Add(30 * time.Second),
		InputTokens:  10000,
		OutputTokens: 5000,
	})

	snap := tracker.TakeSnapshot()

	if snap.TotalInputTokens != 20000 {
		t.Errorf("snapshot input tokens: expected 20000, got %d", snap.TotalInputTokens)
	}
	if snap.TotalOutputTokens != 10000 {
		t.Errorf("snapshot output tokens: expected 10000, got %d", snap.TotalOutputTokens)
	}
	if snap.TotalTokens != 30000 {
		t.Errorf("snapshot total tokens: expected 30000, got %d", snap.TotalTokens)
	}
	if snap.EventCount != 2 {
		t.Errorf("snapshot event count: expected 2, got %d", snap.EventCount)
	}
	if snap.BurnRateTPM <= 0 {
		t.Error("snapshot burn rate should be > 0")
	}
}

func TestSummary(t *testing.T) {
	tracker := New(
		WithPricing(PricingClaudeSonnet46),
		WithBudget(BudgetConfig{
			WarnThresholdUSD:   20.00,
			HardLimitUSD:       25.00,
			MaxTokensPerMinute: 4000,
		}),
	)

	now := time.Now()
	tracker.RecordEvent(TokenEvent{
		Timestamp:    now,
		InputTokens:  5000,
		OutputTokens: 2000,
	})

	summary := tracker.Summary()
	if len(summary) == 0 {
		t.Error("summary should not be empty")
	}
}

func TestModelPricingHelpers(t *testing.T) {
	p := PricingClaudeOpus48

	// 1M input tokens = $5.00
	cost := p.InputCost(1_000_000)
	if diff := cost - 5.00; diff > 0.001 || diff < -0.001 {
		t.Errorf("expected $5.00, got $%.4f", cost)
	}

	// 1M output tokens = $25.00
	cost = p.OutputCost(1_000_000)
	if diff := cost - 25.00; diff > 0.001 || diff < -0.001 {
		t.Errorf("expected $25.00, got $%.4f", cost)
	}
}

func TestBudgetStatusString(t *testing.T) {
	tests := []struct {
		status BudgetStatus
		want   string
	}{
		{BudgetOK, "OK"},
		{BudgetWarning, "WARNING"},
		{BudgetExceeded, "EXCEEDED"},
		{BudgetRateExceeded, "RATE_EXCEEDED"},
		{BudgetStatus(99), "UNKNOWN"},
	}
	for _, tc := range tests {
		if got := tc.status.String(); got != tc.want {
			t.Errorf("BudgetStatus(%d).String() = %q, want %q", tc.status, got, tc.want)
		}
	}
}

func TestTokenEventTotal(t *testing.T) {
	e := TokenEvent{InputTokens: 300, OutputTokens: 200}
	if e.Total() != 500 {
		t.Errorf("expected 500, got %d", e.Total())
	}
}

func TestConcurrentAccess(t *testing.T) {
	tracker := New(
		WithPricing(PricingClaudeSonnet46),
		WithBudget(BudgetConfig{
			WarnThresholdUSD:   100,
			HardLimitUSD:       200,
			MaxTokensPerMinute: 50000,
		}),
	)

	done := make(chan struct{})
	// Writer goroutine
	go func() {
		for i := 0; i < 1000; i++ {
			tracker.RecordEvent(TokenEvent{
				InputTokens:  100,
				OutputTokens: 50,
			})
		}
		close(done)
	}()

	// Reader goroutine (concurrent with writer)
	for i := 0; i < 1000; i++ {
		_ = tracker.BurnRate()
		_ = tracker.CumulativeCost()
		_ = tracker.IsOverBudget()
		_ = tracker.CheckBudget()
		_ = tracker.TakeSnapshot()
	}

	<-done

	if tracker.EventCount() != 1000 {
		t.Errorf("expected 1000 events, got %d", tracker.EventCount())
	}
}
