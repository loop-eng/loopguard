package ltf

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestWriteEvent(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)

	ts := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	err := w.WriteEvent(Event{
		LTFVersion: "1.0",
		LoopID:     "test-loop",
		Timestamp:  ts,
		Phase:      "terminate",
		Action: &EventAction{
			Type:   "circuit_breaker",
			Detail: "budget exceeded",
		},
		CostUSD: 20.12,
		Metadata: map[string]any{
			"source": "loopguard",
			"action": "pause",
		},
	})
	if err != nil {
		t.Fatalf("WriteEvent() error: %v", err)
	}

	var got Event
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if got.LTFVersion != "1.0" {
		t.Errorf("ltf_version = %v, want 1.0", got.LTFVersion)
	}
	if got.LoopID != "test-loop" {
		t.Errorf("loop_id = %v, want test-loop", got.LoopID)
	}
	if got.Phase != "terminate" {
		t.Errorf("phase = %v, want terminate", got.Phase)
	}
	if got.CostUSD != 20.12 {
		t.Errorf("cost_usd = %v, want 20.12", got.CostUSD)
	}
}

func TestWriteEventDefaults(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)

	err := w.WriteEvent(Event{
		LoopID: "test",
		Phase:  "act",
	})
	if err != nil {
		t.Fatalf("WriteEvent() error: %v", err)
	}

	var got Event
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if got.LTFVersion != "1.0" {
		t.Errorf("ltf_version should default to 1.0, got %v", got.LTFVersion)
	}
	if got.Timestamp.IsZero() {
		t.Error("timestamp should be auto-filled")
	}
}
