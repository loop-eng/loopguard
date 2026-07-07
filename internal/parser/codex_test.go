package parser

import (
	"testing"
)

func TestCodexParseToolCall(t *testing.T) {
	line := []byte(`{"type":"tool_call_started","id":"tc-1","session_id":"codex-sess","timestamp":"2026-07-01T10:00:00Z","data":{"name":"shell","input":"{\"command\":\"npm test\"}"}}`)

	p := NewCodexParser()
	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	ev := events[0]
	if ev.ContentType != ContentToolUse {
		t.Errorf("ContentType = %v, want ContentToolUse", ev.ContentType)
	}
	if ev.ToolName != "shell" {
		t.Errorf("ToolName = %v, want shell", ev.ToolName)
	}
}

func TestCodexParseToolResult(t *testing.T) {
	line := []byte(`{"type":"tool_call_ended","id":"tc-1","session_id":"codex-sess","timestamp":"2026-07-01T10:00:05Z","data":{"output":"test passed","is_error":false}}`)

	p := NewCodexParser()
	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ContentType != ContentToolResult {
		t.Errorf("ContentType = %v, want ContentToolResult", events[0].ContentType)
	}
	if events[0].IsError {
		t.Error("should not be error")
	}
}

func TestCodexParseInference(t *testing.T) {
	line := []byte(`{"type":"inference_completed","id":"inf-1","session_id":"codex-sess","timestamp":"2026-07-01T10:00:10Z","data":{"model":"o4-mini","input_tokens":5000,"output_tokens":200,"reasoning_output_tokens":100,"token_count":5300}}`)

	p := NewCodexParser()
	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	ev := events[0]
	if ev.Model != "o4-mini" {
		t.Errorf("Model = %v, want o4-mini", ev.Model)
	}
	if ev.Tokens.InputTokens != 5000 {
		t.Errorf("InputTokens = %d, want 5000", ev.Tokens.InputTokens)
	}
	if ev.Tokens.OutputTokens != 300 {
		t.Errorf("OutputTokens = %d, want 300 (output+reasoning)", ev.Tokens.OutputTokens)
	}
}

func TestCodexSkipsIrrelevantTypes(t *testing.T) {
	lines := [][]byte{
		[]byte(`{"type":"codex_turn_started"}`),
		[]byte(`{"type":"codex_turn_ended"}`),
		[]byte(`{"type":"unknown_event"}`),
	}

	p := NewCodexParser()
	for _, line := range lines {
		events, err := p.Parse(line)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if events != nil {
			t.Errorf("expected nil events for irrelevant type, got %d", len(events))
		}
	}
}
