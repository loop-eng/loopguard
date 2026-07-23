package analyzer

import (
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/loop-eng/loopguard/internal/config"
	"github.com/loop-eng/loopguard/internal/parser"
)

type modelEntry struct {
	name    string
	pricing ModelPricing
}

type CostCalculator struct {
	mu      sync.RWMutex
	pricing map[string]ModelPricing
	sorted  []modelEntry
	logger  *slog.Logger
}

func NewCostCalculator(logger *slog.Logger, overrides map[string]config.PricingOverride) *CostCalculator {
	pricing := DefaultPricing()
	for model, o := range overrides {
		pricing[model] = ModelPricing{
			InputPerMTok:      o.InputPerMTok,
			OutputPerMTok:     o.OutputPerMTok,
			CacheReadPerMTok:  o.CacheReadPerMTok,
			CacheWritePerMTok: o.CacheWritePerMTok,
		}
	}

	sorted := make([]modelEntry, 0, len(pricing))
	for name, p := range pricing {
		sorted = append(sorted, modelEntry{name, p})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return len(sorted[i].name) > len(sorted[j].name)
	})
	return &CostCalculator{
		pricing: pricing,
		sorted:  sorted,
		logger:  logger,
	}
}

// MergePricing rebuilds the pricing table from defaults + new overrides.
// Used by config hot-reload to apply updated user pricing at runtime.
func (cc *CostCalculator) MergePricing(overrides map[string]config.PricingOverride) {
	pricing := DefaultPricing()
	for model, o := range overrides {
		pricing[model] = ModelPricing{
			InputPerMTok:      o.InputPerMTok,
			OutputPerMTok:     o.OutputPerMTok,
			CacheReadPerMTok:  o.CacheReadPerMTok,
			CacheWritePerMTok: o.CacheWritePerMTok,
		}
	}

	sorted := make([]modelEntry, 0, len(pricing))
	for name, p := range pricing {
		sorted = append(sorted, modelEntry{name, p})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return len(sorted[i].name) > len(sorted[j].name)
	})

	cc.mu.Lock()
	cc.pricing = pricing
	cc.sorted = sorted
	cc.mu.Unlock()
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
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	if p, ok := cc.pricing[model]; ok {
		return p
	}

	for _, entry := range cc.sorted {
		if strings.HasPrefix(model, entry.name) {
			return entry.pricing
		}
	}

	cc.logger.Warn("unknown model, using fallback pricing", "model", model)
	return FallbackPricing
}
