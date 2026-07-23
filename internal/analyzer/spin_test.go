package analyzer

import (
	"strings"
	"testing"
	"time"

	"github.com/loop-eng/loopguard/internal/parser"
)

func TestSpinDetectorRepeatedCalls(t *testing.T) {
	sd := NewSpinDetector(SpinConfig{
		RepeatedCalls:      3,
		ErrorEcho:          3,
		StallMinutes:       10,
		CostVelocityPerMin: 2.0,
		WindowSize:         50,
	})

	now := time.Now()

	// Same tool call 3 times
	for i := 0; i < 3; i++ {
		ev := &parser.ParsedEvent{
			ContentType: parser.ContentToolUse,
			ToolName:    "Bash",
			ToolInput:   `{"command":"npm test"}`,
			Timestamp:   now.Add(time.Duration(i) * time.Second),
		}
		result := sd.Check(ev, 1.0)
		if i < 2 && result.IsSpinning {
			t.Errorf("should not spin at call %d", i+1)
		}
		if i == 2 && !result.IsSpinning {
			t.Error("should detect spin at call 3")
		}
	}
}

func TestSpinDetectorNoFalsePositive(t *testing.T) {
	sd := NewSpinDetector(DefaultSpinConfig())
	now := time.Now()

	// Different tool calls should not trigger
	tools := []struct{ name, input string }{
		{"Read", `{"file_path":"/a.go"}`},
		{"Read", `{"file_path":"/b.go"}`},
		{"Edit", `{"file_path":"/c.go"}`},
		{"Bash", `{"command":"go build"}`},
	}

	for i, tc := range tools {
		ev := &parser.ParsedEvent{
			ContentType: parser.ContentToolUse,
			ToolName:    tc.name,
			ToolInput:   tc.input,
			Timestamp:   now.Add(time.Duration(i) * time.Second),
		}
		result := sd.Check(ev, 0.5)
		if result.IsSpinning {
			t.Errorf("false positive at tool %d (%s)", i, tc.name)
		}
	}
}

func TestSpinDetectorErrorEcho(t *testing.T) {
	sd := NewSpinDetector(SpinConfig{
		RepeatedCalls:      10, // high to not trigger
		ErrorEcho:          3,
		StallMinutes:       10,
		CostVelocityPerMin: 100,
		WindowSize:         50,
	})

	for i := 0; i < 3; i++ {
		ev := &parser.ParsedEvent{
			ContentType: parser.ContentToolResult,
			IsError:     true,
			ErrorMsg:    "TypeError: Cannot read property 'foo' of undefined",
			Timestamp:   time.Now(),
		}
		result := sd.Check(ev, 1.0)
		if i < 2 && result.IsSpinning {
			t.Errorf("should not spin at error %d", i+1)
		}
		if i == 2 && !result.IsSpinning {
			t.Error("should detect error echo at error 3")
		}
	}
}

func TestSpinDetectorCostVelocity(t *testing.T) {
	sd := NewSpinDetector(SpinConfig{
		RepeatedCalls:      100,
		ErrorEcho:          100,
		StallMinutes:       100,
		CostVelocityPerMin: 1.0,
		WindowSize:         50,
	})

	// Simulate $5 over 2 minutes = $2.5/min (exceeds $1/min threshold)
	start := time.Now().Add(-2 * time.Minute)

	ev1 := &parser.ParsedEvent{
		ContentType: parser.ContentToolUse,
		ToolName:    "Read",
		ToolInput:   `{"file_path":"/a"}`,
		Timestamp:   start,
		Tokens:      parser.TokenUsage{InputTokens: 1000},
	}
	sd.Check(ev1, 1.0)

	ev2 := &parser.ParsedEvent{
		ContentType: parser.ContentToolUse,
		ToolName:    "Read",
		ToolInput:   `{"file_path":"/b"}`,
		Timestamp:   start.Add(2 * time.Minute),
		Tokens:      parser.TokenUsage{InputTokens: 1000},
	}
	result := sd.Check(ev2, 6.0)

	if !result.IsSpinning {
		t.Error("should detect cost velocity exceeding threshold")
	}
}

func TestSpinDetectorContextBloat(t *testing.T) {
	sd := NewSpinDetector(SpinConfig{
		RepeatedCalls:      100,
		ErrorEcho:          100,
		StallMinutes:       100,
		CostVelocityPerMin: 100,
		ContextFillPercent: 85,
		WindowSize:         50,
	})

	// 80% fill of a 200K window (claude-haiku-4-5) — should not trigger.
	ev := &parser.ParsedEvent{
		ContentType: parser.ContentText,
		Model:       "claude-haiku-4-5",
		Timestamp:   time.Now(),
		Tokens:      parser.TokenUsage{InputTokens: 160_000},
	}
	result := sd.Check(ev, 1.0)
	if result.IsSpinning {
		t.Error("should not trigger at 80% fill")
	}

	// 90% fill — should trigger (threshold 85%).
	ev2 := &parser.ParsedEvent{
		ContentType: parser.ContentText,
		Model:       "claude-haiku-4-5",
		Timestamp:   time.Now(),
		Tokens:      parser.TokenUsage{InputTokens: 180_000},
	}
	result2 := sd.Check(ev2, 1.0)
	if !result2.IsSpinning {
		t.Error("should trigger at 90% fill (threshold 85%)")
	}
	if result2.Heuristic != "context_bloat" {
		t.Errorf("expected heuristic context_bloat, got %q", result2.Heuristic)
	}
}

func TestSpinDetectorContextBloatDisabled(t *testing.T) {
	sd := NewSpinDetector(SpinConfig{
		RepeatedCalls:      100,
		ErrorEcho:          100,
		StallMinutes:       100,
		CostVelocityPerMin: 100,
		ContextFillPercent: 0, // disabled
		WindowSize:         50,
	})

	ev := &parser.ParsedEvent{
		ContentType: parser.ContentText,
		Model:       "claude-haiku-4-5",
		Timestamp:   time.Now(),
		Tokens:      parser.TokenUsage{InputTokens: 199_000}, // 99.5%
	}
	result := sd.Check(ev, 1.0)
	if result.IsSpinning {
		t.Error("should not trigger when ContextFillPercent is 0")
	}
}

func TestSpinDetectorContextBloatFallback(t *testing.T) {
	sd := NewSpinDetector(SpinConfig{
		RepeatedCalls:      100,
		ErrorEcho:          100,
		StallMinutes:       100,
		CostVelocityPerMin: 100,
		ContextFillPercent: 85,
		WindowSize:         50,
	})

	// Unknown model -> falls back to the 200K conservative window.
	ev := &parser.ParsedEvent{
		ContentType: parser.ContentText,
		Model:       "unknown-model-xyz",
		Timestamp:   time.Now(),
		Tokens:      parser.TokenUsage{InputTokens: 180_000}, // 90% of 200K
	}
	result := sd.Check(ev, 1.0)
	if !result.IsSpinning {
		t.Error("should trigger with fallback context window")
	}
}

func TestSpinDetectorContextBloatDatedModelPrefix(t *testing.T) {
	sd := NewSpinDetector(SpinConfig{
		RepeatedCalls:      100,
		ErrorEcho:          100,
		StallMinutes:       100,
		CostVelocityPerMin: 100,
		ContextFillPercent: 85,
		WindowSize:         50,
	})

	// A dated model version should prefix-match "claude-sonnet-4-6" (1M window),
	// not fall back to the conservative 200K window.
	ev := &parser.ParsedEvent{
		ContentType: parser.ContentText,
		Model:       "claude-sonnet-4-6-20260714",
		Timestamp:   time.Now(),
		Tokens:      parser.TokenUsage{InputTokens: 900_000}, // ~85.8% of 1,048,576
	}
	result := sd.Check(ev, 1.0)
	if !result.IsSpinning {
		t.Error("should trigger via longest-prefix match against 1M window, not 200K fallback")
	}

	// Sanity check: 900K tokens is only 450% "full" against the 200K fallback,
	// which would ALSO trigger, so verify the reason cites the larger window.
	if len(result.Reasons) == 0 || !strings.Contains(result.Reasons[len(result.Reasons)-1], "1048576") {
		t.Errorf("expected reason to cite 1048576-token window, got %v", result.Reasons)
	}
}

func TestSpinDetectorBudget(t *testing.T) {
	be := NewBudgetEnforcer(10.0, 50.0, 200.0, 80)

	// Under warning threshold
	r := be.RecordCost("s1", 5.0)
	if r != nil {
		t.Error("should not alert at 50%")
	}

	// At warning threshold (80%)
	r = be.RecordCost("s1", 3.0)
	if r == nil || !r.Warning {
		t.Error("should warn at 80%")
	}

	// Over budget
	r = be.RecordCost("s1", 3.0)
	if r == nil || !r.Exceeded {
		t.Error("should exceed at 110%")
	}
}

func TestBudgetUpdateLimits(t *testing.T) {
	be := NewBudgetEnforcer(10.0, 50.0, 200.0, 80)

	// Record 8.5 — should warn at 80% of 10.0
	r := be.RecordCost("s1", 8.5)
	if r == nil || !r.Warning {
		t.Error("should warn at 85% of $10 limit")
	}

	// Raise the limit to $100 — now 8.5 is only 8.5%
	be.UpdateLimits(100.0, 500.0, 2000.0, 80)

	// Record another small amount on a new session
	r = be.RecordCost("s2", 5.0)
	if r != nil {
		t.Error("should not alert at 5% of $100 limit after UpdateLimits")
	}

	// The old session s1 still has 8.5 recorded — but that's 8.5% of new limit
	r = be.RecordCost("s1", 1.0)
	if r != nil {
		t.Error("s1 total 9.5 should not trigger with new $100 limit")
	}
}
