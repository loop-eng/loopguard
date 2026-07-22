package analyzer

type ModelPricing struct {
	InputPerMTok      float64
	OutputPerMTok     float64
	CacheReadPerMTok  float64
	CacheWritePerMTok float64
}

func DefaultPricing() map[string]ModelPricing {
	return map[string]ModelPricing{
		// Anthropic
		"claude-opus-4-8":   {5.00, 25.00, 0.50, 6.25},
		"claude-opus-4-7":   {5.00, 25.00, 0.50, 6.25},
		"claude-opus-4-6":   {5.00, 25.00, 0.50, 6.25},
		"claude-sonnet-4-6": {3.00, 15.00, 0.30, 3.75},
		"claude-sonnet-4-5": {3.00, 15.00, 0.30, 3.75},
		"claude-haiku-4-5":  {1.00, 5.00, 0.10, 1.25},

		// OpenAI
		"gpt-5.5":      {5.00, 30.00, 0, 0},
		"gpt-4.1":      {2.00, 8.00, 0, 0},
		"gpt-4.1-mini": {0.40, 1.60, 0, 0},
		"o4-mini":      {1.10, 4.40, 0, 0},
		"o3":           {2.00, 8.00, 0, 0},

		// Google
		"gemini-2.5-pro":   {1.25, 10.00, 0, 0},
		"gemini-2.5-flash": {0.15, 0.60, 0, 0},
	}
}

var FallbackPricing = ModelPricing{3.00, 15.00, 0.30, 3.75}

// FallbackContextWindow is used when a model's context window size is
// unknown. It is intentionally conservative (smaller than most modern
// models) so context-bloat detection triggers earlier rather than missing
// a real bloat on an unrecognized model.
const FallbackContextWindow = 200_000

// ModelContextWindows returns the context window size (in tokens) for
// known models. Used by the spin detector's context-bloat heuristic to
// estimate how full the model's context window is from input_tokens.
func ModelContextWindows() map[string]int {
	return map[string]int{
		// Anthropic
		"claude-opus-4-8":   1_048_576,
		"claude-opus-4-7":   1_048_576,
		"claude-opus-4-6":   1_048_576,
		"claude-sonnet-4-6": 1_048_576,
		"claude-sonnet-4-5": 1_048_576,
		"claude-haiku-4-5":  200_000,

		// OpenAI
		"gpt-5.5":      1_048_576,
		"gpt-4.1":      1_047_576,
		"gpt-4.1-mini": 1_047_576,
		"o4-mini":      200_000,
		"o3":           200_000,

		// Google
		"gemini-2.5-pro":   1_048_576,
		"gemini-2.5-flash": 1_048_576,
	}
}
