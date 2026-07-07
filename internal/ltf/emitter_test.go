package ltf

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEmitterIntervention(t *testing.T) {
	dir := t.TempDir()
	e := NewEmitter(slog.Default(), dir, true)
	defer e.Close()

	e.EmitIntervention("sess-123", "claude", "paused", "budget_exceeded", 20.50)

	path := filepath.Join(dir, "sess-123.ltf.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("trace file not created: %v", err)
	}

	var event Event
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("failed to parse event: %v", err)
	}

	if event.LTFVersion != "1.0" {
		t.Errorf("ltf_version = %v, want 1.0", event.LTFVersion)
	}
	if event.LoopID != "sess-123" {
		t.Errorf("loop_id = %v, want sess-123", event.LoopID)
	}
	if event.Phase != "terminate" {
		t.Errorf("phase = %v, want terminate", event.Phase)
	}
	if event.CostUSD != 20.50 {
		t.Errorf("cost_usd = %v, want 20.50", event.CostUSD)
	}
	if event.Action.Type != "circuit_breaker" {
		t.Errorf("action.type = %v, want circuit_breaker", event.Action.Type)
	}
	if event.Metadata["source"] != "loopguard" {
		t.Errorf("metadata.source = %v, want loopguard", event.Metadata["source"])
	}
}

func TestEmitterDisabled(t *testing.T) {
	dir := t.TempDir()
	e := NewEmitter(slog.Default(), dir, false)

	e.EmitIntervention("sess-456", "claude", "paused", "spin", 5.0)

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Error("disabled emitter should not create files")
	}
}

func TestEmitterSessionEnd(t *testing.T) {
	dir := t.TempDir()
	e := NewEmitter(slog.Default(), dir, true)
	defer e.Close()

	start := time.Now()
	e.EmitIntervention("sess-end", "codex", "killed", "budget_exceeded", 50.0)
	e.EmitSessionEnd("sess-end", "codex", "budget_exceeded", 50.0, start)

	path := filepath.Join(dir, "sess-end.ltf.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("trace file not created: %v", err)
	}

	lines := splitJSONL(data)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (event + summary), got %d", len(lines))
	}

	var summary LoopSummary
	if err := json.Unmarshal(lines[1], &summary); err != nil {
		t.Fatalf("failed to parse summary: %v", err)
	}
	if summary.Type != "loop_summary" {
		t.Errorf("type = %v, want loop_summary", summary.Type)
	}
	if summary.TotalCostUSD != 50.0 {
		t.Errorf("total_cost_usd = %v, want 50.0", summary.TotalCostUSD)
	}
}

func TestEmitterMultipleSessions(t *testing.T) {
	dir := t.TempDir()
	e := NewEmitter(slog.Default(), dir, true)
	defer e.Close()

	e.EmitIntervention("sess-a", "claude", "paused", "spin", 10.0)
	e.EmitIntervention("sess-b", "codex", "paused", "budget", 20.0)

	if _, err := os.Stat(filepath.Join(dir, "sess-a.ltf.jsonl")); err != nil {
		t.Error("sess-a trace file not created")
	}
	if _, err := os.Stat(filepath.Join(dir, "sess-b.ltf.jsonl")); err != nil {
		t.Error("sess-b trace file not created")
	}
}

func TestEmitterPathTraversal(t *testing.T) {
	dir := t.TempDir()
	e := NewEmitter(slog.Default(), dir, true)
	defer e.Close()

	e.EmitIntervention("../../etc/evil", "claude", "paused", "spin", 1.0)

	if _, err := os.Stat(filepath.Join(dir, "../../etc/evil.ltf.jsonl")); err == nil {
		t.Error("path traversal should be blocked")
	}
}

func TestAgentProvider(t *testing.T) {
	tests := []struct {
		agent, want string
	}{
		{"claude", "anthropic"},
		{"codex", "openai"},
		{"gemini", "google"},
		{"custom", "unknown"},
	}
	for _, tt := range tests {
		got := agentProvider(tt.agent)
		if got != tt.want {
			t.Errorf("agentProvider(%q) = %q, want %q", tt.agent, got, tt.want)
		}
	}
}

func splitJSONL(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				lines = append(lines, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
