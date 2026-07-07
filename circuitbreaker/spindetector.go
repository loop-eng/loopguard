// Package circuitbreaker implements spin detection for AI agent loops.
//
// It monitors a stream of agent actions (tool calls, edits, errors) and
// detects when an agent is stuck in a repetitive loop — the same edit
// applied over and over, the same error surfacing repeatedly, or similar
// actions that never converge toward a solution.
//
// Detection uses four independent heuristics, each producing a sub-score
// that is combined into a single confidence value (0.0-1.0):
//
//  1. Exact repetition — same action fingerprint N times in a row.
//  2. Fuzzy repetition — similar actions (Levenshtein distance) within
//     the sliding window, catching parameter-tweaked retries.
//  3. No-progress stall — state hash unchanged across consecutive steps.
//  4. Oscillation (ping-pong) — alternating between two actions (A-B-A-B).
//
// Design informed by:
//   - OpenClaw tool-loop detection: (toolName, argsHash, resultHash) triples
//     with warning/critical/circuit-breaker threshold tiers.
//   - Modexa agent loop guard: action fingerprint hashing + no-progress
//     counter + hard step/time/tool-call budgets.
//   - AWS Strands DebounceHook: sliding window of recent fingerprints,
//     blocking duplicate calls before execution.
//   - Floyd's cycle detection insight: O(1) space detection of periodic
//     subsequences in the action stream.
//   - Sony gobreaker circuit breaker state machine: Closed/Open/Half-Open
//     with configurable thresholds and recovery probing.
//
// References:
//   - https://docs.openclaw.ai/tools/loop-detection
//   - https://medium.com/@Modexa/the-agent-loop-problem-when-smart-wont-stop
//   - https://dev.to/aws/how-to-prevent-ai-agent-reasoning-loops
//   - https://en.wikipedia.org/wiki/Cycle_detection
//   - https://github.com/sony/gobreaker
//   - https://github.com/hbollon/go-edlib (Levenshtein in Go)
package circuitbreaker

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Action — the unit of observation
// ---------------------------------------------------------------------------

// Action represents a single agent step: a tool call, a file edit, an error,
// or any other discrete event in an agent loop. The SpinDetector observes a
// stream of these.
type Action struct {
	// Timestamp of the action. Zero value defaults to time.Now() on record.
	Timestamp time.Time

	// Tool is the tool or action type (e.g., "Edit", "Bash", "Read").
	Tool string

	// Args is a human-readable representation of the arguments. For hashing
	// purposes it is normalized (sorted, lowered). Callers should pass a
	// stable string — e.g., sorted JSON of the tool input.
	Args string

	// Result summarizes the outcome (e.g., "success", an error message, or a
	// short hash of the tool output). Used by no-progress detection.
	Result string

	// StateHash is an opaque hash of the "world state" after this action
	// completes — for example, a hash of the files the agent has modified.
	// If empty, the detector derives one from (Tool, Args, Result).
	StateHash string

	// Error, if non-empty, indicates the action produced an error.
	Error string
}

// fingerprint returns a short, stable hash of (Tool, Args) for exact-match
// detection. Modeled on the Modexa pattern:
//
//	sha256(tool + "|" + sorted(args))[:16]
func (a Action) fingerprint() string {
	norm := strings.ToLower(strings.TrimSpace(a.Args))
	data := []byte(strings.ToLower(a.Tool) + "|" + norm)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:8]) // 16 hex chars
}

// stateKey returns the state hash for no-progress tracking. Falls back to
// a hash of (Tool, Args, Result) if StateHash is empty.
func (a Action) stateKey() string {
	if a.StateHash != "" {
		return a.StateHash
	}
	data := []byte(a.Tool + "|" + a.Args + "|" + a.Result)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:8])
}

// signature returns a short human-readable label like "Edit(file.go)".
func (a Action) signature() string {
	args := a.Args
	if len(args) > 60 {
		args = args[:57] + "..."
	}
	return fmt.Sprintf("%s(%s)", a.Tool, args)
}

// ---------------------------------------------------------------------------
// SpinResult — what the detector returns
// ---------------------------------------------------------------------------

// SpinResult is the output of SpinDetector.Analyze(). It reports whether the
// agent appears to be spinning, with a confidence score and human-readable
// explanations.
type SpinResult struct {
	// IsSpinning is true when Confidence >= the configured threshold.
	IsSpinning bool

	// Confidence is the combined spin score, 0.0 (no spin) to 1.0 (certain).
	Confidence float64

	// Reasons lists human-readable explanations of why spin was detected.
	// Empty when Confidence is 0.
	Reasons []string

	// Heuristic names the dominant heuristic that contributed most to the
	// score (e.g., "exact_repeat", "fuzzy_repeat", "no_progress", "oscillation").
	Heuristic string

	// SubScores exposes per-heuristic scores for observability/debugging.
	SubScores map[string]float64

	// ActionCount is the total number of actions observed so far.
	ActionCount int

	// WindowSize is the current number of actions in the sliding window.
	WindowSize int
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// SpinConfig controls thresholds for spin detection.
type SpinConfig struct {
	// WindowSize is the number of recent actions to retain for analysis.
	// Default: 20.
	WindowSize int

	// ExactRepeatThreshold is how many consecutive identical fingerprints
	// trigger spin. Default: 3 (same as OpenClaw and Modexa defaults).
	ExactRepeatThreshold int

	// FuzzySimilarityThreshold is the minimum Levenshtein similarity ratio
	// (0.0-1.0) for two actions to be considered "similar". Default: 0.85.
	// Research shows 0.80-0.90 catches parameter-tweaked retries without
	// false-positiving on legitimately different actions.
	FuzzySimilarityThreshold float64

	// FuzzyRepeatThreshold is how many similar (but not identical) actions
	// within the window trigger spin. Default: 4.
	FuzzyRepeatThreshold int

	// NoProgressThreshold is how many consecutive actions with the same
	// state hash trigger the no-progress heuristic. Default: 5.
	NoProgressThreshold int

	// OscillationThreshold is the minimum number of A-B-A-B alternations
	// to detect ping-pong behavior. Default: 3 (i.e., 6 actions: ABABAB).
	OscillationThreshold int

	// ConfidenceThreshold is the combined score above which IsSpinning is
	// set to true. Default: 0.6.
	ConfidenceThreshold float64

	// ExactRepeatWeight is the weight of the exact-repeat sub-score in the
	// combined confidence. Default: 0.35.
	ExactRepeatWeight float64

	// FuzzyRepeatWeight is the weight of the fuzzy-repeat sub-score.
	// Default: 0.25.
	FuzzyRepeatWeight float64

	// NoProgressWeight is the weight of the no-progress sub-score.
	// Default: 0.25.
	NoProgressWeight float64

	// OscillationWeight is the weight of the oscillation sub-score.
	// Default: 0.15.
	OscillationWeight float64
}

// DefaultSpinConfig returns production-ready defaults derived from the
// research findings:
//   - OpenClaw uses warning=10, critical=20, circuit-breaker=30 in a
//     window of 30. We use a smaller window (20) with lower thresholds
//     (3/4/5) because we want faster detection for interactive agents.
//   - Modexa uses repeat_limit=2, no_progress_limit=2 with a step
//     budget of 10. We're slightly more tolerant to reduce false positives.
//   - AWS Strands DebounceHook blocks after 2 identical calls in a window
//     of 3. We detect at 3 to allow one legitimate retry.
func DefaultSpinConfig() SpinConfig {
	return SpinConfig{
		WindowSize:               20,
		ExactRepeatThreshold:     3,
		FuzzySimilarityThreshold: 0.85,
		FuzzyRepeatThreshold:     4,
		NoProgressThreshold:      5,
		OscillationThreshold:     3,
		ConfidenceThreshold:      0.6,
		ExactRepeatWeight:        0.35,
		FuzzyRepeatWeight:        0.25,
		NoProgressWeight:         0.25,
		OscillationWeight:        0.15,
	}
}

// ---------------------------------------------------------------------------
// SpinDetector
// ---------------------------------------------------------------------------

// SpinDetector monitors a stream of agent actions and detects spin (the agent
// repeating itself without making progress). It is safe for concurrent use.
//
// Usage:
//
//	det := NewSpinDetector(DefaultSpinConfig())
//
//	for action := range agentActions {
//	    result := det.Record(action)
//	    if result.IsSpinning {
//	        log.Printf("SPIN DETECTED (%.0f%% confidence): %s",
//	            result.Confidence*100, strings.Join(result.Reasons, "; "))
//	        // pause agent, inject reflection prompt, escalate, etc.
//	    }
//	}
type SpinDetector struct {
	mu     sync.RWMutex
	config SpinConfig

	// Sliding window of recent actions (circular buffer semantics).
	window []Action
	total  int // total actions ever recorded

	// Consecutive-repeat tracking: fingerprint of the last action and how
	// many times it has repeated without interruption.
	lastFingerprint string
	consecutiveRun  int

	// No-progress tracking: state key of the last action and how many
	// consecutive actions produced the same state.
	lastStateKey       string
	consecutiveNoProgress int

	// Error tracking: most recent error string and consecutive count.
	lastError       string
	consecutiveErrs int
}

// NewSpinDetector creates a SpinDetector with the given configuration.
func NewSpinDetector(cfg SpinConfig) *SpinDetector {
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = 20
	}
	return &SpinDetector{
		config: cfg,
		window: make([]Action, 0, cfg.WindowSize),
	}
}

// Record adds an action to the detector and returns the current spin analysis.
// This is the main entry point — call it after every agent step.
func (d *SpinDetector) Record(a Action) SpinResult {
	if a.Timestamp.IsZero() {
		a.Timestamp = time.Now()
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	fp := a.fingerprint()
	sk := a.stateKey()

	// --- Update consecutive-repeat counter ---
	if fp == d.lastFingerprint {
		d.consecutiveRun++
	} else {
		d.consecutiveRun = 1
		d.lastFingerprint = fp
	}

	// --- Update no-progress counter ---
	if sk == d.lastStateKey {
		d.consecutiveNoProgress++
	} else {
		d.consecutiveNoProgress = 1
		d.lastStateKey = sk
	}

	// --- Update error counter ---
	if a.Error != "" {
		if a.Error == d.lastError {
			d.consecutiveErrs++
		} else {
			d.consecutiveErrs = 1
			d.lastError = a.Error
		}
	} else {
		d.consecutiveErrs = 0
		d.lastError = ""
	}

	// --- Append to sliding window ---
	if len(d.window) >= d.config.WindowSize {
		// Shift left (drop oldest).
		copy(d.window, d.window[1:])
		d.window = d.window[:d.config.WindowSize-1]
	}
	d.window = append(d.window, a)
	d.total++

	// --- Run heuristics ---
	return d.analyze()
}

// Analyze returns the current spin assessment without recording a new action.
func (d *SpinDetector) Analyze() SpinResult {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.analyze()
}

// Reset clears all internal state, as if the detector were freshly created.
func (d *SpinDetector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.window = d.window[:0]
	d.total = 0
	d.lastFingerprint = ""
	d.consecutiveRun = 0
	d.lastStateKey = ""
	d.consecutiveNoProgress = 0
	d.lastError = ""
	d.consecutiveErrs = 0
}

// RecentActions returns a copy of the sliding window.
func (d *SpinDetector) RecentActions() []Action {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]Action, len(d.window))
	copy(out, d.window)
	return out
}

// TotalActions returns the total number of actions ever recorded.
func (d *SpinDetector) TotalActions() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.total
}

// ---------------------------------------------------------------------------
// Heuristic implementations (all called under lock)
// ---------------------------------------------------------------------------

// analyze runs all four heuristics and combines their scores.
func (d *SpinDetector) analyze() SpinResult {
	scores := map[string]float64{
		"exact_repeat": d.scoreExactRepeat(),
		"fuzzy_repeat": d.scoreFuzzyRepeat(),
		"no_progress":  d.scoreNoProgress(),
		"oscillation":  d.scoreOscillation(),
	}

	// Weighted combination
	combined := scores["exact_repeat"]*d.config.ExactRepeatWeight +
		scores["fuzzy_repeat"]*d.config.FuzzyRepeatWeight +
		scores["no_progress"]*d.config.NoProgressWeight +
		scores["oscillation"]*d.config.OscillationWeight

	// Clamp to [0, 1]
	if combined > 1.0 {
		combined = 1.0
	}

	// Find dominant heuristic
	dominant := "none"
	maxScore := 0.0
	for name, score := range scores {
		if score > maxScore {
			maxScore = score
			dominant = name
		}
	}

	// Build reasons
	var reasons []string
	if scores["exact_repeat"] > 0 {
		reasons = append(reasons, fmt.Sprintf(
			"exact repeat: same action %d times in a row (threshold %d)",
			d.consecutiveRun, d.config.ExactRepeatThreshold))
	}
	if scores["fuzzy_repeat"] > 0 {
		reasons = append(reasons, fmt.Sprintf(
			"fuzzy repeat: %.0f%% similar actions detected in window",
			scores["fuzzy_repeat"]*100))
	}
	if scores["no_progress"] > 0 {
		reasons = append(reasons, fmt.Sprintf(
			"no progress: state unchanged for %d consecutive actions (threshold %d)",
			d.consecutiveNoProgress, d.config.NoProgressThreshold))
	}
	if scores["oscillation"] > 0 {
		reasons = append(reasons, "oscillation: ping-pong pattern detected (A-B-A-B)")
	}
	if d.consecutiveErrs >= d.config.ExactRepeatThreshold {
		reasons = append(reasons, fmt.Sprintf(
			"error echo: same error repeated %d times", d.consecutiveErrs))
	}

	// Sort reasons for deterministic output
	sort.Strings(reasons)

	return SpinResult{
		IsSpinning:  combined >= d.config.ConfidenceThreshold,
		Confidence:  combined,
		Reasons:     reasons,
		Heuristic:   dominant,
		SubScores:   scores,
		ActionCount: d.total,
		WindowSize:  len(d.window),
	}
}

// scoreExactRepeat scores 0.0-1.0 based on how many consecutive identical
// actions have been observed vs. the threshold.
//
// Algorithm: maintain a running count of consecutive identical fingerprints.
// Score = min(1.0, consecutiveRun / threshold).
// Returns 0 if below threshold; ramps linearly from threshold to 2*threshold.
//
// This is the simplest and most reliable heuristic — if the agent calls the
// same tool with the same args 3+ times in a row, it is almost certainly
// stuck. Research (Modexa, OpenClaw) confirms that 3 identical calls in one
// task "is definitionally a loop."
func (d *SpinDetector) scoreExactRepeat() float64 {
	if d.consecutiveRun < d.config.ExactRepeatThreshold {
		return 0.0
	}
	// Linear ramp: at threshold -> 0.5, at 2*threshold -> 1.0
	ratio := float64(d.consecutiveRun) / float64(d.config.ExactRepeatThreshold*2)
	if ratio > 1.0 {
		return 1.0
	}
	return ratio
}

// scoreFuzzyRepeat uses Levenshtein similarity to find near-duplicate actions
// in the sliding window.
//
// Algorithm: for each pair of actions in the window, compute the Levenshtein
// similarity ratio of their signatures (Tool+Args). Count how many actions
// are "similar" to the most recent action. Score based on the fraction of
// similar actions vs. the fuzzy threshold.
//
// This catches the pattern where an agent tweaks parameters slightly on each
// retry — e.g., editing the same file at line 42, then 43, then 42 again.
// The AWS DebounceHook only catches exact duplicates; fuzzy matching catches
// the parameter-variation bypass (which reduced DebounceHook effectiveness
// from 14 to only 12 calls in AWS testing, vs. 2 with clear terminal states).
func (d *SpinDetector) scoreFuzzyRepeat() float64 {
	if len(d.window) < 2 {
		return 0.0
	}

	latest := d.window[len(d.window)-1]
	latestSig := latest.signature()
	similarCount := 0

	for i := 0; i < len(d.window)-1; i++ {
		sig := d.window[i].signature()
		sim := levenshteinSimilarity(latestSig, sig)
		if sim >= d.config.FuzzySimilarityThreshold {
			similarCount++
		}
	}

	if similarCount < d.config.FuzzyRepeatThreshold {
		return 0.0
	}

	// Score: fraction of similar actions in window, scaled
	ratio := float64(similarCount) / float64(len(d.window)-1)
	if ratio > 1.0 {
		return 1.0
	}
	return ratio
}

// scoreNoProgress detects when consecutive actions produce the same state.
//
// Algorithm: track the state hash (or derived hash from Tool+Args+Result)
// across consecutive actions. If the hash is unchanged for N steps, the
// agent is making no forward progress.
//
// This is the "progress detection" mechanism described in the Modexa article:
// "hash the tool name plus its arguments on each step, keep a short window
// of recent hashes, and bail when you see a repeat." We extend it with a
// state hash that captures the world state, not just the action signature.
func (d *SpinDetector) scoreNoProgress() float64 {
	if d.consecutiveNoProgress < d.config.NoProgressThreshold {
		return 0.0
	}
	// Linear ramp: at threshold -> 0.5, at 2*threshold -> 1.0
	ratio := float64(d.consecutiveNoProgress) / float64(d.config.NoProgressThreshold*2)
	if ratio > 1.0 {
		return 1.0
	}
	return ratio
}

// scoreOscillation detects A-B-A-B ping-pong patterns in the window.
//
// Algorithm: examine the last 2*OscillationThreshold actions. If they
// alternate between exactly two fingerprints (A-B-A-B-A-B), score based
// on the length of the alternation.
//
// This catches the "oscillation loop" pattern described in the Modexa
// article — harder to detect than simple repetition because each individual
// step looks different from the previous one. OpenClaw's `pingPong` detector
// targets the same pattern.
//
// The Floyd/Brent cycle detection insight applies here: instead of hashing
// every possible subsequence, we can detect cycles by comparing elements at
// specific offsets. For ping-pong specifically, checking window[i] == window[i-2]
// for consecutive positions is equivalent and O(n).
func (d *SpinDetector) scoreOscillation() float64 {
	minLen := d.config.OscillationThreshold * 2
	if len(d.window) < minLen {
		return 0.0
	}

	// Check if the last N actions alternate between two fingerprints.
	tail := d.window[len(d.window)-minLen:]

	fpA := tail[0].fingerprint()
	fpB := tail[1].fingerprint()

	// A and B must be different
	if fpA == fpB {
		return 0.0
	}

	alternations := 0
	for i := 0; i < len(tail); i++ {
		expected := fpA
		if i%2 == 1 {
			expected = fpB
		}
		if tail[i].fingerprint() == expected {
			alternations++
		}
	}

	// Perfect alternation: all positions match
	ratio := float64(alternations) / float64(len(tail))
	if ratio < 0.8 {
		return 0.0 // not enough alternation to be a real ping-pong
	}

	// Score based on how perfect the alternation is
	return ratio
}

// ---------------------------------------------------------------------------
// Levenshtein distance — inlined to avoid external dependencies
// ---------------------------------------------------------------------------

// levenshteinDistance computes the minimum edit distance between two strings.
// Uses the single-column space-optimized dynamic programming approach.
//
// Algorithm: standard Wagner-Fischer with O(min(m,n)) space. The insight is
// that each cell in the DP matrix depends only on the cell above, to the
// left, and diagonally above-left — so we can compute column by column using
// a single slice, tracking the diagonal value ("lastkey") as we go.
//
// Time: O(m*n), Space: O(min(m,n)).
//
// Implementation adapted from Hugo Bollon's go-edlib (MIT License).
// Reference: https://dev.to/hbollon/introduction-to-string-edit-distance-and-levenshtein-implementation-in-golang-2l99
func levenshteinDistance(s1, s2 string) int {
	r1 := []rune(s1)
	r2 := []rune(s2)

	n := len(r1)
	m := len(r2)

	if n == 0 {
		return m
	}
	if m == 0 {
		return n
	}

	// Ensure r1 is the shorter string for space optimization.
	if n > m {
		r1, r2 = r2, r1
		n, m = m, n
	}

	// Single-column DP.
	col := make([]int, n+1)
	for i := 1; i <= n; i++ {
		col[i] = i
	}

	for j := 1; j <= m; j++ {
		col[0] = j
		lastDiag := j - 1
		for i := 1; i <= n; i++ {
			old := col[i]
			cost := 0
			if r1[i-1] != r2[j-1] {
				cost = 1
			}
			// min(insert, delete, substitute)
			ins := col[i] + 1
			del := col[i-1] + 1
			sub := lastDiag + cost

			best := ins
			if del < best {
				best = del
			}
			if sub < best {
				best = sub
			}
			col[i] = best
			lastDiag = old
		}
	}
	return col[n]
}

// levenshteinSimilarity returns a similarity ratio in [0.0, 1.0] between
// two strings, where 1.0 means identical and 0.0 means completely different.
//
// Formula: (maxLen - distance) / maxLen
//
// This is the standard normalization used by go-edlib's StringsSimilarity()
// and Python's SequenceMatcher.ratio().
func levenshteinSimilarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	ra := []rune(a)
	rb := []rune(b)
	maxLen := len(ra)
	if len(rb) > maxLen {
		maxLen = len(rb)
	}
	if maxLen == 0 {
		return 1.0
	}
	dist := levenshteinDistance(a, b)
	return float64(maxLen-dist) / float64(maxLen)
}

// ---------------------------------------------------------------------------
// Convenience constructors
// ---------------------------------------------------------------------------

// NewAction creates an Action with minimal fields. Use this for quick
// prototyping; production code should populate all relevant fields.
func NewAction(tool, args string) Action {
	return Action{
		Timestamp: time.Now(),
		Tool:      tool,
		Args:      args,
	}
}

// NewActionWithResult creates an Action with tool, args, and result.
func NewActionWithResult(tool, args, result string) Action {
	return Action{
		Timestamp: time.Now(),
		Tool:      tool,
		Args:      args,
		Result:    result,
	}
}

// NewActionWithError creates an Action that represents an error.
func NewActionWithError(tool, args, errMsg string) Action {
	return Action{
		Timestamp: time.Now(),
		Tool:      tool,
		Args:      args,
		Error:     errMsg,
	}
}
