package analyzer

import (
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
