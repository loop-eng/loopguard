package analyzer

import (
	"log/slog"
	"strings"

	"github.com/loop-eng/loopguard/internal/parser"
)

type CostCalculator struct {
	pricing map[string]ModelPricing
	logger  *slog.Logger
}

func NewCostCalculator(logger *slog.Logger) *CostCalculator {
	return &CostCalculator{
		pricing: DefaultPricing(),
		logger:  logger,
	}
}

func (cc *CostCalculator) Calculate(usage parser.TokenUsage, model string) float64 {
	p := cc.resolve(model)

	cost := float64(usage.InputTokens) * p.InputPerMTok / 1_000_000
	cost += float64(usage.OutputTokens) * p.OutputPerMTok / 1_000_000
	cost += float64(usage.CacheReadTokens) * p.CacheReadPerMTok / 1_000_000
	cost += float64(usage.CacheWriteTokens) * p.CacheWritePerMTok / 1_000_000

	return cost
}

func (cc *CostCalculator) resolve(model string) ModelPricing {
	if p, ok := cc.pricing[model]; ok {
		return p
	}

	// Try prefix matching (e.g., "claude-sonnet-4-6[1m]" → "claude-sonnet-4-6")
	for name, p := range cc.pricing {
		if strings.HasPrefix(model, name) {
			return p
		}
	}

	cc.logger.Warn("unknown model, using fallback pricing", "model", model)
	return FallbackPricing
}
