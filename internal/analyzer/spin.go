package analyzer

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/loop-eng/loopguard/internal/parser"
)

type SpinResult struct {
	IsSpinning bool
	Reasons    []string
	Heuristic  string // which heuristic triggered first
}

type SpinDetector struct {
	cfg SpinConfig

	// Circular buffer of recent tool calls
	recentTools []toolFingerprint
	toolHead    int
	toolCount   int

	// Error tracking
	recentErrors []string
	errorHead    int
	errorCount   int

	// Progress tracking
	lastFileEdit time.Time
	hasActivity  bool

	// Cost velocity
	costWindow []timedCost

	// Context bloat tracking
	contextWindows       map[string]int       // exact model name -> context window size
	sortedContextEntries []contextWindowEntry // same data, sorted by name length desc for prefix matching
	lastModel            string               // most recently seen model name
}

type SpinConfig struct {
	RepeatedCalls      int
	ErrorEcho          int
	StallMinutes       int
	CostVelocityPerMin float64
	ContextFillPercent int // threshold percentage (e.g. 85); 0 disables the check
	WindowSize         int // circular buffer size
}

type contextWindowEntry struct {
	name   string
	window int
}

type toolFingerprint struct {
	hash      string
	timestamp time.Time
}

type timedCost struct {
	timestamp time.Time
	cost      float64
}

func DefaultSpinConfig() SpinConfig {
	return SpinConfig{
		RepeatedCalls:      3,
		ErrorEcho:          3,
		StallMinutes:       10,
		CostVelocityPerMin: 2.0,
		ContextFillPercent: 85,
		WindowSize:         50,
	}
}

func NewSpinDetector(cfg SpinConfig) *SpinDetector {
	windows := ModelContextWindows()
	sorted := make([]contextWindowEntry, 0, len(windows))
	for name, w := range windows {
		sorted = append(sorted, contextWindowEntry{name: name, window: w})
	}
	// Sort by name length descending for deterministic longest-prefix matching
	// (map iteration order is random; without this, a versioned model name
	// like "claude-sonnet-4-6-20260714" could non-deterministically match a
	// shorter unrelated prefix).
	sort.Slice(sorted, func(i, j int) bool {
		return len(sorted[i].name) > len(sorted[j].name)
	})

	return &SpinDetector{
		cfg:                  cfg,
		recentTools:          make([]toolFingerprint, cfg.WindowSize),
		recentErrors:         make([]string, cfg.WindowSize),
		contextWindows:       windows,
		sortedContextEntries: sorted,
	}
}

func (sd *SpinDetector) Check(event *parser.ParsedEvent, sessionCost float64) SpinResult {
	var result SpinResult

	switch event.ContentType {
	case parser.ContentToolUse:
		sd.recordTool(event)
		if r := sd.checkRepeatedTools(); r != "" {
			result.IsSpinning = true
			result.Reasons = append(result.Reasons, r)
			if result.Heuristic == "" {
				result.Heuristic = "repeated_tool_calls"
			}
		}

		if parser.IsFileModifyingTool(event.ToolName) {
			sd.lastFileEdit = event.Timestamp
		}

	case parser.ContentToolResult:
		if event.IsError {
			sd.recordError(event.ErrorMsg)
			if r := sd.checkErrorEcho(); r != "" {
				result.IsSpinning = true
				result.Reasons = append(result.Reasons, r)
				if result.Heuristic == "" {
					result.Heuristic = "error_echo"
				}
			}
		}
	}

	// Track activity for stall detection
	if event.Tokens.Total() > 0 {
		sd.hasActivity = true
	}

	// Stall detection
	if r := sd.checkStall(event.Timestamp); r != "" {
		result.Reasons = append(result.Reasons, r)
		// Stall is a warning, not a hard spin
	}

	// Cost velocity
	if sessionCost > 0 {
		sd.recordCost(event.Timestamp, sessionCost)
		if r := sd.checkCostVelocity(); r != "" {
			result.IsSpinning = true
			result.Reasons = append(result.Reasons, r)
			if result.Heuristic == "" {
				result.Heuristic = "cost_velocity"
			}
		}
	}

	// Context bloat detection
	if event.Tokens.InputTokens > 0 {
		if r := sd.checkContextBloat(event); r != "" {
			result.IsSpinning = true
			result.Reasons = append(result.Reasons, r)
			if result.Heuristic == "" {
				result.Heuristic = "context_bloat"
			}
		}
	}

	return result
}

func (sd *SpinDetector) recordTool(event *parser.ParsedEvent) {
	hash := fingerprint(event.ToolName, event.ToolInput)
	sd.recentTools[sd.toolHead] = toolFingerprint{hash: hash, timestamp: event.Timestamp}
	sd.toolHead = (sd.toolHead + 1) % sd.cfg.WindowSize
	if sd.toolCount < sd.cfg.WindowSize {
		sd.toolCount++
	}
}

func (sd *SpinDetector) checkRepeatedTools() string {
	if sd.toolCount < sd.cfg.RepeatedCalls {
		return ""
	}

	// Count occurrences of each fingerprint in the buffer
	counts := make(map[string]int)
	for i := 0; i < sd.toolCount; i++ {
		h := sd.recentTools[i].hash
		if h != "" {
			counts[h]++
		}
	}

	for _, count := range counts {
		if count >= sd.cfg.RepeatedCalls {
			return fmt.Sprintf("same tool call repeated %d times (threshold: %d)", count, sd.cfg.RepeatedCalls)
		}
	}
	return ""
}

func (sd *SpinDetector) recordError(errMsg string) {
	normalized := normalizeError(errMsg)
	sd.recentErrors[sd.errorHead] = normalized
	sd.errorHead = (sd.errorHead + 1) % sd.cfg.WindowSize
	if sd.errorCount < sd.cfg.WindowSize {
		sd.errorCount++
	}
}

func (sd *SpinDetector) checkErrorEcho() string {
	if sd.errorCount < sd.cfg.ErrorEcho {
		return ""
	}

	counts := make(map[string]int)
	for i := 0; i < sd.errorCount; i++ {
		e := sd.recentErrors[i]
		if e != "" {
			counts[e]++
		}
	}

	for _, count := range counts {
		if count >= sd.cfg.ErrorEcho {
			return fmt.Sprintf("same error repeated %d times (threshold: %d)", count, sd.cfg.ErrorEcho)
		}
	}
	return ""
}

func (sd *SpinDetector) checkStall(now time.Time) string {
	if !sd.hasActivity || sd.lastFileEdit.IsZero() {
		return ""
	}

	stallDuration := time.Duration(sd.cfg.StallMinutes) * time.Minute
	if now.Sub(sd.lastFileEdit) > stallDuration {
		return fmt.Sprintf("no file modifications for %d minutes despite ongoing activity", sd.cfg.StallMinutes)
	}
	return ""
}

func (sd *SpinDetector) recordCost(ts time.Time, cost float64) {
	sd.costWindow = append(sd.costWindow, timedCost{timestamp: ts, cost: cost})

	// Trim window to last 5 minutes (copy to release old backing array)
	cutoff := ts.Add(-5 * time.Minute)
	trimIdx := 0
	for trimIdx < len(sd.costWindow) && sd.costWindow[trimIdx].timestamp.Before(cutoff) {
		trimIdx++
	}
	if trimIdx > 0 {
		remaining := make([]timedCost, len(sd.costWindow)-trimIdx)
		copy(remaining, sd.costWindow[trimIdx:])
		sd.costWindow = remaining
	}
}

func (sd *SpinDetector) checkCostVelocity() string {
	if len(sd.costWindow) < 2 {
		return ""
	}

	first := sd.costWindow[0]
	last := sd.costWindow[len(sd.costWindow)-1]

	elapsed := last.timestamp.Sub(first.timestamp).Minutes()
	if elapsed < 1.0 {
		return ""
	}

	costDelta := last.cost - first.cost
	velocity := costDelta / elapsed

	if velocity > sd.cfg.CostVelocityPerMin {
		return fmt.Sprintf("cost velocity $%.2f/min exceeds threshold $%.2f/min", velocity, sd.cfg.CostVelocityPerMin)
	}
	return ""
}

// checkContextBloat estimates how full the model's context window is using
// the input_tokens of the current event (the full prompt sent to the model
// for this turn, including accumulated conversation history). It returns a
// human-readable reason string if the fill percentage meets or exceeds the
// configured threshold, or "" otherwise.
func (sd *SpinDetector) checkContextBloat(event *parser.ParsedEvent) string {
	if sd.cfg.ContextFillPercent <= 0 {
		return ""
	}

	if event.Model != "" {
		sd.lastModel = event.Model
	}

	contextWindow := sd.resolveContextWindow(sd.lastModel)
	inputTokens := event.Tokens.InputTokens

	fillPct := (inputTokens * 100) / contextWindow
	if fillPct >= sd.cfg.ContextFillPercent {
		return fmt.Sprintf("context window %d%% full (%d/%d tokens, threshold: %d%%)",
			fillPct, inputTokens, contextWindow, sd.cfg.ContextFillPercent)
	}
	return ""
}

// resolveContextWindow finds the context window size for a model name,
// trying an exact match first, then the longest known prefix (to handle
// dated model versions like "claude-sonnet-4-6-20260714"), falling back to
// a conservative default when the model is unrecognized.
func (sd *SpinDetector) resolveContextWindow(model string) int {
	if model == "" {
		return FallbackContextWindow
	}
	if w, ok := sd.contextWindows[model]; ok {
		return w
	}
	for _, entry := range sd.sortedContextEntries {
		if strings.HasPrefix(model, entry.name) {
			return entry.window
		}
	}
	return FallbackContextWindow
}

func fingerprint(toolName, toolInput string) string {
	h := sha256.Sum256([]byte(toolName + "|" + toolInput))
	return fmt.Sprintf("%x", h[:8])
}

func normalizeError(msg string) string {
	s := strings.TrimSpace(msg)
	if len(s) > 200 {
		s = s[:200]
	}
	return strings.ToLower(s)
}
