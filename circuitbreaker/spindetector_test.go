package circuitbreaker

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Levenshtein distance tests
// ---------------------------------------------------------------------------

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"kitten", "sitting", 3},
		{"saturday", "sunday", 3},
		{"abc", "axc", 1},
		{"abc", "abcd", 1},
		{"flaw", "lawn", 2},
	}
	for _, tc := range tests {
		got := levenshteinDistance(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("levenshteinDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestLevenshteinDistanceUnicode(t *testing.T) {
	// Verify rune-level (not byte-level) comparison.
	got := levenshteinDistance("café", "cafe")
	if got != 1 {
		t.Errorf("expected distance 1 for cafe\\u0301 vs cafe, got %d", got)
	}
}

func TestLevenshteinSimilarity(t *testing.T) {
	tests := []struct {
		a, b    string
		wantMin float64
		wantMax float64
	}{
		{"abc", "abc", 1.0, 1.0},
		{"", "", 1.0, 1.0},
		{"abc", "xyz", 0.0, 0.01},
		{"kitten", "sitten", 0.8, 0.9},
		{"Edit(file.go:42)", "Edit(file.go:43)", 0.9, 1.0},
	}
	for _, tc := range tests {
		got := levenshteinSimilarity(tc.a, tc.b)
		if got < tc.wantMin || got > tc.wantMax {
			t.Errorf("levenshteinSimilarity(%q, %q) = %.3f, want [%.2f, %.2f]",
				tc.a, tc.b, got, tc.wantMin, tc.wantMax)
		}
	}
}

// ---------------------------------------------------------------------------
// Action tests
// ---------------------------------------------------------------------------

func TestActionFingerprint(t *testing.T) {
	a := NewAction("Edit", `{"file": "main.go", "line": 42}`)
	b := NewAction("Edit", `{"file": "main.go", "line": 42}`)
	c := NewAction("Edit", `{"file": "main.go", "line": 43}`)

	if a.fingerprint() != b.fingerprint() {
		t.Error("identical actions should have same fingerprint")
	}
	if a.fingerprint() == c.fingerprint() {
		t.Error("different actions should have different fingerprints")
	}
}

func TestActionFingerprintCaseInsensitive(t *testing.T) {
	a := NewAction("EDIT", "File.Go")
	b := NewAction("edit", "file.go")
	if a.fingerprint() != b.fingerprint() {
		t.Error("fingerprints should be case-insensitive")
	}
}

func TestActionStateKey(t *testing.T) {
	a := Action{Tool: "Edit", Args: "file.go", Result: "ok"}
	b := Action{Tool: "Edit", Args: "file.go", Result: "ok"}
	c := Action{Tool: "Edit", Args: "file.go", Result: "error"}

	if a.stateKey() != b.stateKey() {
		t.Error("same (tool, args, result) should have same state key")
	}
	if a.stateKey() == c.stateKey() {
		t.Error("different results should have different state keys")
	}
}

func TestActionStateKeyExplicit(t *testing.T) {
	a := Action{Tool: "Edit", Args: "x", StateHash: "custom-hash-123"}
	if a.stateKey() != "custom-hash-123" {
		t.Error("explicit StateHash should be used as state key")
	}
}

func TestActionSignature(t *testing.T) {
	a := NewAction("Edit", "file.go")
	sig := a.signature()
	if sig != "Edit(file.go)" {
		t.Errorf("unexpected signature: %q", sig)
	}
}

func TestActionSignatureTruncation(t *testing.T) {
	longArgs := ""
	for i := 0; i < 100; i++ {
		longArgs += "x"
	}
	a := NewAction("Edit", longArgs)
	sig := a.signature()
	if len(sig) > 70 { // "Edit(" + 57 + "..." + ")"
		t.Errorf("signature should truncate long args, got len %d: %q", len(sig), sig)
	}
}

// ---------------------------------------------------------------------------
// SpinDetector — exact repeat tests
// ---------------------------------------------------------------------------

func TestNoSpinOnDiverseActions(t *testing.T) {
	det := NewSpinDetector(DefaultSpinConfig())

	actions := []Action{
		NewAction("Read", "file1.go"),
		NewAction("Edit", "file2.go"),
		NewAction("Bash", "go test ./..."),
		NewAction("Read", "file3.go"),
		NewAction("Edit", "file4.go"),
	}

	var result SpinResult
	for _, a := range actions {
		result = det.Record(a)
	}

	if result.IsSpinning {
		t.Errorf("diverse actions should not trigger spin, got confidence %.2f: %v",
			result.Confidence, result.Reasons)
	}
	if result.ActionCount != 5 {
		t.Errorf("expected 5 actions, got %d", result.ActionCount)
	}
}

func TestExactRepeatDetection(t *testing.T) {
	det := NewSpinDetector(DefaultSpinConfig())

	// Record the same action 6 times (well above threshold of 3).
	for i := 0; i < 6; i++ {
		det.Record(NewAction("Edit", `{"file":"main.go","line":42}`))
	}

	result := det.Analyze()
	if result.SubScores["exact_repeat"] <= 0 {
		t.Errorf("expected exact_repeat score > 0, got %.2f", result.SubScores["exact_repeat"])
	}
	if result.Confidence <= 0 {
		t.Errorf("expected confidence > 0, got %.2f", result.Confidence)
	}
}

func TestExactRepeatBelowThreshold(t *testing.T) {
	det := NewSpinDetector(DefaultSpinConfig())

	// Only 2 repeats — below default threshold of 3.
	det.Record(NewAction("Edit", "file.go"))
	det.Record(NewAction("Edit", "file.go"))

	result := det.Analyze()
	if result.SubScores["exact_repeat"] > 0 {
		t.Errorf("2 repeats should not trigger exact_repeat, got %.2f",
			result.SubScores["exact_repeat"])
	}
}

func TestExactRepeatResetOnDifferentAction(t *testing.T) {
	det := NewSpinDetector(DefaultSpinConfig())

	// 2 repeats, then a different action, then 2 more repeats.
	det.Record(NewAction("Edit", "file.go"))
	det.Record(NewAction("Edit", "file.go"))
	det.Record(NewAction("Read", "other.go")) // breaks the streak
	det.Record(NewAction("Edit", "file.go"))
	det.Record(NewAction("Edit", "file.go"))

	result := det.Analyze()
	if result.SubScores["exact_repeat"] > 0 {
		t.Errorf("interrupted repeat should not trigger, got %.2f",
			result.SubScores["exact_repeat"])
	}
}

func TestExactRepeatHighConfidence(t *testing.T) {
	cfg := DefaultSpinConfig()
	cfg.ExactRepeatThreshold = 3
	det := NewSpinDetector(cfg)

	// 10 identical actions: well past 2*threshold=6
	for i := 0; i < 10; i++ {
		det.Record(NewAction("Edit", "same_file.go"))
	}

	result := det.Analyze()
	if result.SubScores["exact_repeat"] < 0.9 {
		t.Errorf("10 repeats should produce high exact_repeat score, got %.2f",
			result.SubScores["exact_repeat"])
	}
	if !result.IsSpinning {
		t.Error("10 identical actions should trigger IsSpinning")
	}
}

// ---------------------------------------------------------------------------
// SpinDetector — fuzzy repeat tests
// ---------------------------------------------------------------------------

func TestFuzzyRepeatDetection(t *testing.T) {
	cfg := DefaultSpinConfig()
	cfg.WindowSize = 10
	cfg.FuzzyRepeatThreshold = 3
	cfg.FuzzySimilarityThreshold = 0.80
	det := NewSpinDetector(cfg)

	// Similar but not identical actions — agent tweaking line numbers.
	det.Record(NewAction("Edit", "main.go:line42"))
	det.Record(NewAction("Edit", "main.go:line43"))
	det.Record(NewAction("Edit", "main.go:line44"))
	det.Record(NewAction("Edit", "main.go:line42"))
	det.Record(NewAction("Edit", "main.go:line45"))

	result := det.Analyze()
	if result.SubScores["fuzzy_repeat"] <= 0 {
		t.Errorf("similar actions should trigger fuzzy_repeat, got %.2f",
			result.SubScores["fuzzy_repeat"])
	}
}

func TestFuzzyRepeatNotTriggeredByDifferentTools(t *testing.T) {
	cfg := DefaultSpinConfig()
	cfg.FuzzyRepeatThreshold = 3
	det := NewSpinDetector(cfg)

	// Different tools, different args — low similarity.
	det.Record(NewAction("Read", "package.json"))
	det.Record(NewAction("Edit", "main.go"))
	det.Record(NewAction("Bash", "go build ./..."))
	det.Record(NewAction("Write", "output.txt"))

	result := det.Analyze()
	if result.SubScores["fuzzy_repeat"] > 0 {
		t.Errorf("diverse actions should not trigger fuzzy_repeat, got %.2f",
			result.SubScores["fuzzy_repeat"])
	}
}

// ---------------------------------------------------------------------------
// SpinDetector — no-progress tests
// ---------------------------------------------------------------------------

func TestNoProgressDetection(t *testing.T) {
	cfg := DefaultSpinConfig()
	cfg.NoProgressThreshold = 3
	det := NewSpinDetector(cfg)

	// Same state hash repeated.
	for i := 0; i < 5; i++ {
		det.Record(Action{
			Timestamp: time.Now(),
			Tool:      "Edit",
			Args:      "file.go",
			Result:    "same error: undefined variable",
			StateHash: "deadbeef",
		})
	}

	result := det.Analyze()
	if result.SubScores["no_progress"] <= 0 {
		t.Errorf("same state hash should trigger no_progress, got %.2f",
			result.SubScores["no_progress"])
	}
}

func TestNoProgressResetOnChange(t *testing.T) {
	cfg := DefaultSpinConfig()
	cfg.NoProgressThreshold = 3
	det := NewSpinDetector(cfg)

	// 2 same states, then a change, then 2 more.
	det.Record(Action{Tool: "Edit", Args: "a", StateHash: "aaa"})
	det.Record(Action{Tool: "Edit", Args: "a", StateHash: "aaa"})
	det.Record(Action{Tool: "Edit", Args: "b", StateHash: "bbb"}) // progress!
	det.Record(Action{Tool: "Edit", Args: "a", StateHash: "aaa"})
	det.Record(Action{Tool: "Edit", Args: "a", StateHash: "aaa"})

	result := det.Analyze()
	if result.SubScores["no_progress"] > 0 {
		t.Errorf("progress in between should reset no_progress, got %.2f",
			result.SubScores["no_progress"])
	}
}

func TestNoProgressWithDerivedHash(t *testing.T) {
	cfg := DefaultSpinConfig()
	cfg.NoProgressThreshold = 3
	det := NewSpinDetector(cfg)

	// No explicit StateHash — derived from (Tool, Args, Result).
	for i := 0; i < 5; i++ {
		det.Record(NewActionWithResult("Bash", "go test", "FAIL"))
	}

	result := det.Analyze()
	if result.SubScores["no_progress"] <= 0 {
		t.Errorf("repeated (tool, args, result) should trigger no_progress, got %.2f",
			result.SubScores["no_progress"])
	}
}

// ---------------------------------------------------------------------------
// SpinDetector — oscillation tests
// ---------------------------------------------------------------------------

func TestOscillationDetection(t *testing.T) {
	cfg := DefaultSpinConfig()
	cfg.OscillationThreshold = 3
	det := NewSpinDetector(cfg)

	// A-B-A-B-A-B pattern (6 actions, 3 alternations).
	for i := 0; i < 6; i++ {
		if i%2 == 0 {
			det.Record(NewAction("Edit", "file.go"))
		} else {
			det.Record(NewAction("Bash", "go test"))
		}
	}

	result := det.Analyze()
	if result.SubScores["oscillation"] <= 0 {
		t.Errorf("A-B-A-B pattern should trigger oscillation, got %.2f",
			result.SubScores["oscillation"])
	}
}

func TestNoOscillationWithThreeDistinctActions(t *testing.T) {
	cfg := DefaultSpinConfig()
	cfg.OscillationThreshold = 3
	det := NewSpinDetector(cfg)

	// A-B-C-A-B-C — three-way cycle, not a ping-pong.
	actions := []Action{
		NewAction("Read", "a.go"),
		NewAction("Edit", "b.go"),
		NewAction("Bash", "c.sh"),
		NewAction("Read", "a.go"),
		NewAction("Edit", "b.go"),
		NewAction("Bash", "c.sh"),
	}
	for _, a := range actions {
		det.Record(a)
	}

	result := det.Analyze()
	if result.SubScores["oscillation"] > 0 {
		t.Errorf("three-way cycle should not trigger oscillation, got %.2f",
			result.SubScores["oscillation"])
	}
}

func TestOscillationRequiresMinimumLength(t *testing.T) {
	cfg := DefaultSpinConfig()
	cfg.OscillationThreshold = 3
	det := NewSpinDetector(cfg)

	// Only 4 actions: A-B-A-B (2 alternations, below threshold of 3).
	det.Record(NewAction("Edit", "file.go"))
	det.Record(NewAction("Bash", "go test"))
	det.Record(NewAction("Edit", "file.go"))
	det.Record(NewAction("Bash", "go test"))

	result := det.Analyze()
	if result.SubScores["oscillation"] > 0 {
		t.Errorf("too few alternations should not trigger, got %.2f",
			result.SubScores["oscillation"])
	}
}

// ---------------------------------------------------------------------------
// SpinDetector — combined confidence tests
// ---------------------------------------------------------------------------

func TestCombinedConfidenceThreshold(t *testing.T) {
	cfg := DefaultSpinConfig()
	cfg.ConfidenceThreshold = 0.5
	cfg.ExactRepeatThreshold = 2
	det := NewSpinDetector(cfg)

	// 4 identical actions — triggers exact repeat strongly.
	for i := 0; i < 4; i++ {
		det.Record(NewActionWithResult("Edit", "file.go", "same output"))
	}

	result := det.Analyze()
	if !result.IsSpinning {
		t.Errorf("should be spinning with confidence %.2f (threshold %.2f)",
			result.Confidence, cfg.ConfidenceThreshold)
	}
	if result.Heuristic == "none" {
		t.Error("dominant heuristic should not be 'none'")
	}
}

func TestMultipleHeuristicsCompound(t *testing.T) {
	cfg := DefaultSpinConfig()
	cfg.ExactRepeatThreshold = 2
	cfg.NoProgressThreshold = 2
	cfg.ConfidenceThreshold = 0.3
	det := NewSpinDetector(cfg)

	// Same action, same state — triggers both exact_repeat and no_progress.
	for i := 0; i < 4; i++ {
		det.Record(Action{
			Tool:      "Bash",
			Args:      "go test",
			Result:    "FAIL: compilation error",
			StateHash: "same-hash",
		})
	}

	result := det.Analyze()
	if result.SubScores["exact_repeat"] <= 0 {
		t.Error("exact_repeat should be triggered")
	}
	if result.SubScores["no_progress"] <= 0 {
		t.Error("no_progress should be triggered")
	}
	if !result.IsSpinning {
		t.Errorf("combined heuristics should trigger spin, confidence=%.2f", result.Confidence)
	}
}

// ---------------------------------------------------------------------------
// SpinDetector — error echo tests
// ---------------------------------------------------------------------------

func TestErrorEchoInReasons(t *testing.T) {
	cfg := DefaultSpinConfig()
	cfg.ExactRepeatThreshold = 3
	det := NewSpinDetector(cfg)

	// Same error 5 times.
	for i := 0; i < 5; i++ {
		det.Record(NewActionWithError("Bash", "go build", "undefined: foo"))
	}

	result := det.Analyze()
	found := false
	for _, reason := range result.Reasons {
		if len(reason) > 0 {
			found = true
		}
	}
	if !found {
		t.Error("expected at least one reason for repeated errors")
	}
}

func TestErrorCounterResetsOnSuccess(t *testing.T) {
	cfg := DefaultSpinConfig()
	cfg.ExactRepeatThreshold = 3
	det := NewSpinDetector(cfg)

	det.Record(NewActionWithError("Bash", "go build", "error1"))
	det.Record(NewActionWithError("Bash", "go build", "error1"))
	det.Record(NewAction("Bash", "go build"))                    // success — no error
	det.Record(NewActionWithError("Bash", "go build", "error1")) // restart

	result := det.Analyze()
	// After the success, consecutive errors reset to 1.
	hasErrorEcho := false
	for _, r := range result.Reasons {
		if strings.Contains(r, "error echo") {
			hasErrorEcho = true
		}
	}
	if hasErrorEcho {
		t.Error("error echo should not trigger after a successful action in between")
	}
}

// ---------------------------------------------------------------------------
// SpinDetector — sliding window tests
// ---------------------------------------------------------------------------

func TestSlidingWindowEviction(t *testing.T) {
	cfg := DefaultSpinConfig()
	cfg.WindowSize = 5
	det := NewSpinDetector(cfg)

	for i := 0; i < 10; i++ {
		det.Record(NewAction("Tool", fmt.Sprintf("arg%d", i)))
	}

	actions := det.RecentActions()
	if len(actions) != 5 {
		t.Errorf("expected window of 5, got %d", len(actions))
	}

	// Should have actions 5-9 (the last 5).
	if actions[0].Args != "arg5" {
		t.Errorf("expected first window action to be arg5, got %s", actions[0].Args)
	}
	if actions[4].Args != "arg9" {
		t.Errorf("expected last window action to be arg9, got %s", actions[4].Args)
	}
}

func TestTotalActionsCount(t *testing.T) {
	det := NewSpinDetector(DefaultSpinConfig())

	for i := 0; i < 100; i++ {
		det.Record(NewAction("Tool", fmt.Sprintf("arg%d", i)))
	}

	if det.TotalActions() != 100 {
		t.Errorf("expected 100 total actions, got %d", det.TotalActions())
	}
}

// ---------------------------------------------------------------------------
// SpinDetector — reset tests
// ---------------------------------------------------------------------------

func TestReset(t *testing.T) {
	det := NewSpinDetector(DefaultSpinConfig())

	for i := 0; i < 10; i++ {
		det.Record(NewAction("Edit", "same_file.go"))
	}

	// Should be spinning before reset.
	result := det.Analyze()
	if !result.IsSpinning {
		t.Error("expected spinning before reset")
	}

	det.Reset()

	// Should not be spinning after reset.
	result = det.Analyze()
	if result.IsSpinning {
		t.Error("expected not spinning after reset")
	}
	if det.TotalActions() != 0 {
		t.Errorf("expected 0 actions after reset, got %d", det.TotalActions())
	}
	if len(det.RecentActions()) != 0 {
		t.Errorf("expected empty window after reset, got %d", len(det.RecentActions()))
	}
}

// ---------------------------------------------------------------------------
// SpinDetector — concurrent safety
// ---------------------------------------------------------------------------

func TestConcurrentAccess(t *testing.T) {
	det := NewSpinDetector(DefaultSpinConfig())
	done := make(chan struct{})

	// Writer goroutine.
	go func() {
		for i := 0; i < 1000; i++ {
			det.Record(NewAction("Tool", fmt.Sprintf("arg%d", i%10)))
		}
		close(done)
	}()

	// Reader goroutine (concurrent with writer).
	for i := 0; i < 1000; i++ {
		_ = det.Analyze()
		_ = det.RecentActions()
		_ = det.TotalActions()
	}

	<-done

	if det.TotalActions() != 1000 {
		t.Errorf("expected 1000 total actions, got %d", det.TotalActions())
	}
}

// ---------------------------------------------------------------------------
// SpinDetector — real-world scenario tests
// ---------------------------------------------------------------------------

func TestScenario_ClaudeCodeSameEditLoop(t *testing.T) {
	// Scenario: Claude Code keeps applying the same edit to a file,
	// getting the same lint error, and retrying.
	cfg := DefaultSpinConfig()
	cfg.ExactRepeatThreshold = 3
	cfg.NoProgressThreshold = 3
	det := NewSpinDetector(cfg)

	for i := 0; i < 8; i++ {
		det.Record(Action{
			Tool:   "Edit",
			Args:   `{"file":"src/main.go","old":"foo","new":"bar"}`,
			Result: "applied",
		})
		det.Record(Action{
			Tool:   "Bash",
			Args:   "go vet ./...",
			Result: "src/main.go:42: undefined: bar",
			Error:  "exit status 1",
		})
	}

	result := det.Analyze()
	if !result.IsSpinning {
		t.Errorf("edit-then-error loop should trigger spin, confidence=%.2f, reasons=%v",
			result.Confidence, result.Reasons)
	}
}

func TestScenario_GradualProgress(t *testing.T) {
	// Scenario: the agent is making real progress — different files,
	// different commands, different results.
	det := NewSpinDetector(DefaultSpinConfig())

	steps := []Action{
		{Tool: "Read", Args: "main.go", Result: "200 lines"},
		{Tool: "Edit", Args: "main.go:10", Result: "ok", StateHash: "h1"},
		{Tool: "Bash", Args: "go build", Result: "ok", StateHash: "h2"},
		{Tool: "Read", Args: "handler.go", Result: "150 lines"},
		{Tool: "Edit", Args: "handler.go:25", Result: "ok", StateHash: "h3"},
		{Tool: "Bash", Args: "go test ./...", Result: "PASS", StateHash: "h4"},
		{Tool: "Read", Args: "config.go", Result: "80 lines"},
		{Tool: "Edit", Args: "config.go:5", Result: "ok", StateHash: "h5"},
		{Tool: "Bash", Args: "go build", Result: "ok", StateHash: "h6"},
	}

	var result SpinResult
	for _, a := range steps {
		a.Timestamp = time.Now()
		result = det.Record(a)
	}

	if result.IsSpinning {
		t.Errorf("gradual progress should NOT trigger spin, confidence=%.2f, reasons=%v",
			result.Confidence, result.Reasons)
	}
}

func TestScenario_AgentRetryingWithSlightVariation(t *testing.T) {
	// Scenario: agent keeps trying the same fix with minor variations —
	// should be caught by fuzzy detection.
	cfg := DefaultSpinConfig()
	cfg.WindowSize = 10
	cfg.FuzzyRepeatThreshold = 3
	cfg.FuzzySimilarityThreshold = 0.75
	det := NewSpinDetector(cfg)

	variations := []string{
		`{"file":"main.go","line":42,"col":10}`,
		`{"file":"main.go","line":43,"col":10}`,
		`{"file":"main.go","line":42,"col":11}`,
		`{"file":"main.go","line":44,"col":10}`,
		`{"file":"main.go","line":42,"col":12}`,
		`{"file":"main.go","line":43,"col":11}`,
		`{"file":"main.go","line":42,"col":10}`,
	}

	for _, args := range variations {
		det.Record(NewAction("Edit", args))
	}

	result := det.Analyze()
	if result.SubScores["fuzzy_repeat"] <= 0 {
		t.Errorf("slight variations should trigger fuzzy_repeat, got %.2f",
			result.SubScores["fuzzy_repeat"])
	}
}

// ---------------------------------------------------------------------------
// SpinResult tests
// ---------------------------------------------------------------------------

func TestSpinResultReasons(t *testing.T) {
	cfg := DefaultSpinConfig()
	cfg.ExactRepeatThreshold = 2
	cfg.ConfidenceThreshold = 0.1
	det := NewSpinDetector(cfg)

	for i := 0; i < 5; i++ {
		det.Record(NewActionWithResult("Edit", "file.go", "same"))
	}

	result := det.Analyze()
	if len(result.Reasons) == 0 {
		t.Error("expected at least one reason when spinning")
	}
}

func TestSpinResultSubScores(t *testing.T) {
	det := NewSpinDetector(DefaultSpinConfig())

	det.Record(NewAction("Read", "a.go"))
	result := det.Analyze()

	expectedKeys := []string{"exact_repeat", "fuzzy_repeat", "no_progress", "oscillation"}
	for _, key := range expectedKeys {
		if _, ok := result.SubScores[key]; !ok {
			t.Errorf("SubScores missing key %q", key)
		}
	}
}

// ---------------------------------------------------------------------------
// DefaultSpinConfig tests
// ---------------------------------------------------------------------------

func TestDefaultConfigWeightsSumToOne(t *testing.T) {
	cfg := DefaultSpinConfig()
	sum := cfg.ExactRepeatWeight + cfg.FuzzyRepeatWeight +
		cfg.NoProgressWeight + cfg.OscillationWeight

	if sum < 0.99 || sum > 1.01 {
		t.Errorf("weights should sum to ~1.0, got %.2f", sum)
	}
}

func TestDefaultConfigThresholdsPositive(t *testing.T) {
	cfg := DefaultSpinConfig()
	if cfg.WindowSize <= 0 {
		t.Error("WindowSize must be positive")
	}
	if cfg.ExactRepeatThreshold <= 0 {
		t.Error("ExactRepeatThreshold must be positive")
	}
	if cfg.FuzzyRepeatThreshold <= 0 {
		t.Error("FuzzyRepeatThreshold must be positive")
	}
	if cfg.NoProgressThreshold <= 0 {
		t.Error("NoProgressThreshold must be positive")
	}
	if cfg.OscillationThreshold <= 0 {
		t.Error("OscillationThreshold must be positive")
	}
	if cfg.ConfidenceThreshold <= 0 || cfg.ConfidenceThreshold > 1.0 {
		t.Error("ConfidenceThreshold must be in (0, 1]")
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkRecord(b *testing.B) {
	det := NewSpinDetector(DefaultSpinConfig())
	action := NewAction("Edit", `{"file":"main.go","line":42}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		det.Record(action)
	}
}

func BenchmarkRecordDiverse(b *testing.B) {
	det := NewSpinDetector(DefaultSpinConfig())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		det.Record(NewAction("Tool", fmt.Sprintf("arg%d", i)))
	}
}

func BenchmarkLevenshteinDistance(b *testing.B) {
	s1 := "Edit(main.go:line42:col10)"
	s2 := "Edit(main.go:line43:col11)"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		levenshteinDistance(s1, s2)
	}
}
