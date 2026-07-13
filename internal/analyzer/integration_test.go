package analyzer

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/loop-eng/loopguard/internal/parser"
)

func TestIntegrationSpinDetectionPausesSession(t *testing.T) {
	budget := NewBudgetEnforcer(100, 100, 100, 80)
	a := New(slog.Default(), budget, SpinConfig{
		RepeatedCalls:      3,
		ErrorEcho:          3,
		StallMinutes:       10,
		CostVelocityPerMin: 100,
		WindowSize:         50,
	})

	ctx := context.Background()

	for i := 0; i < 5; i++ {
		a.Process(ctx, "s1", &parser.ParsedEvent{
			ContentType: parser.ContentToolUse,
			ToolName:    "Bash",
			ToolInput:   `{"command":"npm test"}`,
			Timestamp:   time.Now(),
			Tokens:      parser.TokenUsage{InputTokens: 1000, OutputTokens: 100},
			Model:       "claude-sonnet-4-6",
		})
	}

	var alerts []Alert
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case alert := <-a.Alerts():
				alerts = append(alerts, alert)
			case <-time.After(100 * time.Millisecond):
				return
			}
		}
	}()
	<-done

	if len(alerts) == 0 {
		t.Fatal("expected spin detection alert, got none")
	}
	if alerts[0].Trigger != "spin_detected" {
		t.Errorf("expected trigger=spin_detected, got %s", alerts[0].Trigger)
	}
	if alerts[0].SessionID != "s1" {
		t.Errorf("expected session=s1, got %s", alerts[0].SessionID)
	}
	if alerts[0].Level != AlertPause {
		t.Errorf("expected level=pause, got %s", alerts[0].Level)
	}
}

func TestIntegrationBudgetWarningThenExceeded(t *testing.T) {
	budget := NewBudgetEnforcer(1.0, 100, 100, 80)
	a := New(slog.Default(), budget, SpinConfig{
		RepeatedCalls:      100,
		ErrorEcho:          100,
		StallMinutes:       100,
		CostVelocityPerMin: 100,
		WindowSize:         50,
	})

	ctx := context.Background()

	for i := 0; i < 30; i++ {
		a.Process(ctx, "s1", &parser.ParsedEvent{
			ContentType: parser.ContentText,
			Timestamp:   time.Now(),
			Tokens:      parser.TokenUsage{InputTokens: 10000, OutputTokens: 1000},
			Model:       "claude-sonnet-4-6",
		})
	}

	var alerts []Alert
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case alert := <-a.Alerts():
				alerts = append(alerts, alert)
			case <-time.After(100 * time.Millisecond):
				return
			}
		}
	}()
	<-done

	var gotWarning, gotExceeded bool
	for _, alert := range alerts {
		if alert.Trigger == "budget_warning" {
			gotWarning = true
		}
		if alert.Trigger == "budget_exceeded" {
			gotExceeded = true
		}
	}

	if !gotWarning {
		t.Error("expected budget_warning alert")
	}
	if !gotExceeded {
		t.Error("expected budget_exceeded alert")
	}
}

func TestIntegrationBudgetPriorityExceededOverWarning(t *testing.T) {
	budget := NewBudgetEnforcer(10, 0.5, 100, 80)
	a := New(slog.Default(), budget, SpinConfig{
		RepeatedCalls:      100,
		ErrorEcho:          100,
		StallMinutes:       100,
		CostVelocityPerMin: 100,
		WindowSize:         50,
	})

	ctx := context.Background()

	for i := 0; i < 20; i++ {
		a.Process(ctx, "s1", &parser.ParsedEvent{
			ContentType: parser.ContentText,
			Timestamp:   time.Now(),
			Tokens:      parser.TokenUsage{InputTokens: 10000, OutputTokens: 1000},
			Model:       "claude-sonnet-4-6",
		})
	}

	var alerts []Alert
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case alert := <-a.Alerts():
				alerts = append(alerts, alert)
			case <-time.After(100 * time.Millisecond):
				return
			}
		}
	}()
	<-done

	for _, alert := range alerts {
		if alert.Trigger == "budget_exceeded" && alert.Level == AlertPause {
			return
		}
	}
	t.Error("expected budget_exceeded alert with level=pause (per_hour should beat per_session warning)")
}

func TestIntegrationErrorEchoDetection(t *testing.T) {
	budget := NewBudgetEnforcer(100, 100, 100, 80)
	a := New(slog.Default(), budget, SpinConfig{
		RepeatedCalls:      100,
		ErrorEcho:          3,
		StallMinutes:       100,
		CostVelocityPerMin: 100,
		WindowSize:         50,
	})

	ctx := context.Background()

	for i := 0; i < 5; i++ {
		a.Process(ctx, "s1", &parser.ParsedEvent{
			ContentType: parser.ContentToolResult,
			IsError:     true,
			ErrorMsg:    "TypeError: Cannot read property 'foo' of undefined",
			Timestamp:   time.Now(),
		})
	}

	var alerts []Alert
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case alert := <-a.Alerts():
				alerts = append(alerts, alert)
			case <-time.After(100 * time.Millisecond):
				return
			}
		}
	}()
	<-done

	if len(alerts) == 0 {
		t.Fatal("expected error echo alert, got none")
	}
	if alerts[0].Trigger != "spin_detected" {
		t.Errorf("expected trigger=spin_detected, got %s", alerts[0].Trigger)
	}
}

func TestIntegrationNoFalsePositiveOnVariedCalls(t *testing.T) {
	budget := NewBudgetEnforcer(100, 100, 100, 80)
	a := New(slog.Default(), budget, SpinConfig{
		RepeatedCalls:      3,
		ErrorEcho:          3,
		StallMinutes:       100,
		CostVelocityPerMin: 100,
		WindowSize:         50,
	})

	ctx := context.Background()
	tools := []struct{ name, input string }{
		{"Read", `{"file_path":"/a.go"}`},
		{"Read", `{"file_path":"/b.go"}`},
		{"Edit", `{"file_path":"/c.go"}`},
		{"Bash", `{"command":"go build"}`},
		{"Bash", `{"command":"go test"}`},
	}

	for _, tc := range tools {
		a.Process(ctx, "s1", &parser.ParsedEvent{
			ContentType: parser.ContentToolUse,
			ToolName:    tc.name,
			ToolInput:   tc.input,
			Timestamp:   time.Now(),
			Tokens:      parser.TokenUsage{InputTokens: 1000, OutputTokens: 100},
			Model:       "claude-sonnet-4-6",
		})
	}

	select {
	case alert := <-a.Alerts():
		t.Errorf("unexpected alert: %s %s", alert.Trigger, alert.Message)
	case <-time.After(100 * time.Millisecond):
		// good — no alerts
	}
}

func TestIntegrationCostTracking(t *testing.T) {
	budget := NewBudgetEnforcer(100, 100, 100, 80)
	a := New(slog.Default(), budget, SpinConfig{
		RepeatedCalls:      100,
		ErrorEcho:          100,
		StallMinutes:       100,
		CostVelocityPerMin: 100,
		WindowSize:         50,
	})

	ctx := context.Background()
	a.Process(ctx, "s1", &parser.ParsedEvent{
		ContentType: parser.ContentText,
		Timestamp:   time.Now(),
		Tokens:      parser.TokenUsage{InputTokens: 1_000_000, OutputTokens: 0},
		Model:       "claude-opus-4-6",
	})

	cost := a.SessionCost("s1")
	expected := 5.0 // 1M × $5/M
	if cost < expected-0.01 || cost > expected+0.01 {
		t.Errorf("cost = %f, expected %f", cost, expected)
	}
}

func TestIntegrationRemoveSession(t *testing.T) {
	budget := NewBudgetEnforcer(100, 100, 100, 80)
	a := New(slog.Default(), budget, SpinConfig{
		RepeatedCalls: 100, ErrorEcho: 100, StallMinutes: 100,
		CostVelocityPerMin: 100, WindowSize: 50,
	})

	ctx := context.Background()
	a.Process(ctx, "s1", &parser.ParsedEvent{
		ContentType: parser.ContentText,
		Timestamp:   time.Now(),
		Tokens:      parser.TokenUsage{InputTokens: 1000, OutputTokens: 100},
		Model:       "claude-sonnet-4-6",
	})

	if a.SessionCost("s1") == 0 {
		t.Fatal("expected non-zero cost before removal")
	}

	a.RemoveSession("s1")

	if a.SessionCost("s1") != 0 {
		t.Error("expected zero cost after removal")
	}
}

func TestIntegrationStopIdempotent(t *testing.T) {
	budget := NewBudgetEnforcer(100, 100, 100, 80)
	a := New(slog.Default(), budget, SpinConfig{
		RepeatedCalls: 100, ErrorEcho: 100, StallMinutes: 100,
		CostVelocityPerMin: 100, WindowSize: 50,
	})

	a.Stop()
	a.Stop()
	a.Stop()
}

func TestIntegrationCostVelocityDetection(t *testing.T) {
	budget := NewBudgetEnforcer(9999, 9999, 9999, 80)
	a := New(slog.Default(), budget, SpinConfig{
		RepeatedCalls:      100,
		ErrorEcho:          100,
		StallMinutes:       100,
		CostVelocityPerMin: 1.0,
		WindowSize:         50,
	})

	ctx := context.Background()
	start := time.Now().Add(-2 * time.Minute)

	for i := 0; i < 10; i++ {
		ts := start.Add(time.Duration(i) * 15 * time.Second)
		a.Process(ctx, "s1", &parser.ParsedEvent{
			ContentType: parser.ContentText,
			Timestamp:   ts,
			Tokens:      parser.TokenUsage{InputTokens: 100000, OutputTokens: 5000},
			Model:       "claude-opus-4-6",
		})
	}

	var alerts []Alert
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case alert := <-a.Alerts():
				alerts = append(alerts, alert)
			case <-time.After(100 * time.Millisecond):
				return
			}
		}
	}()
	<-done

	for _, alert := range alerts {
		if alert.Trigger == "spin_detected" && contains(alert.Message, "cost velocity") {
			return
		}
	}
	t.Error("expected cost velocity alert")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
