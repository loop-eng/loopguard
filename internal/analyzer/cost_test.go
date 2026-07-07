package analyzer

import (
	"log/slog"
	"math"
	"testing"

	"github.com/loop-eng/loopguard/internal/parser"
)

func TestCostCalculatorSonnet(t *testing.T) {
	cc := NewCostCalculator(slog.Default())

	usage := parser.TokenUsage{
		InputTokens:      10000,
		OutputTokens:     500,
		CacheReadTokens:  5000,
		CacheWriteTokens: 2000,
	}

	cost := cc.Calculate(usage, "claude-sonnet-4-6")

	// Input:  10000 * 3.00 / 1M = 0.030
	// Output: 500 * 15.00 / 1M = 0.0075
	// CacheR: 5000 * 0.30 / 1M = 0.0015
	// CacheW: 2000 * 3.75 / 1M = 0.0075
	expected := 0.030 + 0.0075 + 0.0015 + 0.0075

	if math.Abs(cost-expected) > 0.0001 {
		t.Errorf("cost = %f, want %f", cost, expected)
	}
}

func TestCostCalculatorOpus(t *testing.T) {
	cc := NewCostCalculator(slog.Default())

	usage := parser.TokenUsage{
		InputTokens:  50000,
		OutputTokens: 1000,
	}

	cost := cc.Calculate(usage, "claude-opus-4-6")
	// Input: 50000 * 5.00 / 1M = 0.25
	// Output: 1000 * 25.00 / 1M = 0.025
	expected := 0.275

	if math.Abs(cost-expected) > 0.0001 {
		t.Errorf("cost = %f, want %f", cost, expected)
	}
}

func TestCostCalculatorFallback(t *testing.T) {
	cc := NewCostCalculator(slog.Default())

	usage := parser.TokenUsage{InputTokens: 1000, OutputTokens: 100}
	cost := cc.Calculate(usage, "unknown-model-v99")

	// Should use Sonnet fallback pricing
	expected := float64(1000)*3.00/1_000_000 + float64(100)*15.00/1_000_000
	if math.Abs(cost-expected) > 0.0001 {
		t.Errorf("cost = %f, want %f (fallback)", cost, expected)
	}
}

func TestCostCalculatorPrefixMatch(t *testing.T) {
	cc := NewCostCalculator(slog.Default())

	// Model with suffix (e.g., fast mode)
	usage := parser.TokenUsage{InputTokens: 1000000, OutputTokens: 0}
	cost := cc.Calculate(usage, "claude-opus-4-6[1m]")

	expected := 5.00 // 1M * $5/M
	if math.Abs(cost-expected) > 0.01 {
		t.Errorf("cost = %f, want %f (prefix match)", cost, expected)
	}
}
