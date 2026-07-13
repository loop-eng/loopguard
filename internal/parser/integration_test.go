package parser

import (
	"testing"
)

func TestClaudeParserDedup(t *testing.T) {
	p := NewClaudeParser()

	line := []byte(`{"type":"assistant","uuid":"u1","requestId":"r1","sessionId":"s1","timestamp":"2026-07-10T10:00:00Z","message":{"role":"assistant","model":"claude-opus-4-6","content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":1000,"output_tokens":100}}}`)

	events1, err := p.Parse(line)
	if err != nil {
		t.Fatalf("first parse: %v", err)
	}
	if len(events1) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events1))
	}
	if events1[0].Tokens.Total() == 0 {
		t.Error("first parse should have tokens")
	}

	events2, err := p.Parse(line)
	if err != nil {
		t.Fatalf("second parse: %v", err)
	}
	if len(events2) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events2))
	}
	if events2[0].Tokens.Total() != 0 {
		t.Error("second parse should have zero tokens (deduped)")
	}
}

func TestClaudeParserGenerationalEviction(t *testing.T) {
	p := NewClaudeParser()

	for i := 0; i < maxSeenRequests+100; i++ {
		line := []byte(`{"type":"assistant","uuid":"u1","requestId":"r` + itoa(i) + `","sessionId":"s1","timestamp":"2026-07-10T10:00:00Z","message":{"role":"assistant","model":"claude-opus-4-6","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":100,"output_tokens":10}}}`)
		_, err := p.Parse(line)
		if err != nil {
			t.Fatalf("parse %d: %v", i, err)
		}
	}

	if p.currentCount > maxSeenRequests {
		t.Errorf("currentCount should have been reset after eviction, got %d", p.currentCount)
	}
}

func TestClaudeParserToolResult(t *testing.T) {
	p := NewClaudeParser()

	line := []byte(`{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-07-10T10:00:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"some output","is_error":false}]}}`)

	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ContentType != ContentToolResult {
		t.Errorf("expected ContentToolResult, got %d", events[0].ContentType)
	}
	if events[0].ToolResult != "some output" {
		t.Errorf("expected 'some output', got %q", events[0].ToolResult)
	}
}

func TestClaudeParserErrorToolResult(t *testing.T) {
	p := NewClaudeParser()

	line := []byte(`{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-07-10T10:00:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"TypeError: boom","is_error":true}]}}`)

	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !events[0].IsError {
		t.Error("expected IsError=true")
	}
	if events[0].ErrorMsg != "TypeError: boom" {
		t.Errorf("expected error msg, got %q", events[0].ErrorMsg)
	}
}

func TestClaudeParserNilContent(t *testing.T) {
	p := NewClaudeParser()

	line := []byte(`{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-07-10T10:00:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":null,"is_error":true}]}}`)

	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if events[0].ErrorMsg != "" {
		t.Errorf("nil content should produce empty error msg, got %q", events[0].ErrorMsg)
	}
}

func TestClaudeParserUnknownType(t *testing.T) {
	p := NewClaudeParser()

	line := []byte(`{"type":"system","uuid":"u1","sessionId":"s1","timestamp":"2026-07-10T10:00:00Z","message":{"role":"system","content":[]}}`)

	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if events != nil {
		t.Errorf("unknown type should return nil events, got %d", len(events))
	}
}

func TestClaudeParserMultipleContentBlocks(t *testing.T) {
	p := NewClaudeParser()

	line := []byte(`{"type":"assistant","uuid":"u1","requestId":"r1","sessionId":"s1","timestamp":"2026-07-10T10:00:00Z","message":{"role":"assistant","model":"claude-opus-4-6","content":[{"type":"text","text":"I'll edit"},{"type":"tool_use","name":"Edit","id":"t1","input":{"file_path":"/a.go"}}],"usage":{"input_tokens":5000,"output_tokens":500}}}`)

	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Tokens.Total() == 0 {
		t.Error("first content block should have tokens")
	}
	if events[1].Tokens.Total() != 0 {
		t.Error("second content block should have zero tokens (avoid double-count)")
	}
}

func TestCodexParserToolCall(t *testing.T) {
	p := NewCodexParser()

	line := []byte(`{"type":"tool_call_started","id":"tc1","session_id":"s1","timestamp":"2026-07-10T10:00:00Z","data":{"name":"shell","input":"npm test"}}`)

	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ContentType != ContentToolUse {
		t.Errorf("expected ContentToolUse, got %d", events[0].ContentType)
	}
	if events[0].ToolName != "shell" {
		t.Errorf("expected tool name 'shell', got %q", events[0].ToolName)
	}
}

func TestCodexParserInference(t *testing.T) {
	p := NewCodexParser()

	line := []byte(`{"type":"inference_completed","id":"inf1","session_id":"s1","timestamp":"2026-07-10T10:00:00Z","data":{"model":"gpt-4.1","input_tokens":5000,"output_tokens":500,"reasoning_output_tokens":100}}`)

	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if events[0].Model != "gpt-4.1" {
		t.Errorf("expected model gpt-4.1, got %s", events[0].Model)
	}
	if events[0].Tokens.InputTokens != 5000 {
		t.Errorf("expected 5000 input tokens, got %d", events[0].Tokens.InputTokens)
	}
	if events[0].Tokens.OutputTokens != 600 {
		t.Errorf("expected 600 output tokens (500+100 reasoning), got %d", events[0].Tokens.OutputTokens)
	}
}

func TestCodexParserMalformedData(t *testing.T) {
	p := NewCodexParser()

	line := []byte(`{"type":"tool_call_started","id":"tc1","session_id":"s1","timestamp":"2026-07-10T10:00:00Z","data":"not-json-object"}`)

	_, err := p.Parse(line)
	if err == nil {
		t.Error("expected error for malformed data field")
	}
}

func TestCodexParserDedup(t *testing.T) {
	p := NewCodexParser()

	line := []byte(`{"type":"inference_completed","id":"inf1","session_id":"s1","timestamp":"2026-07-10T10:00:00Z","data":{"model":"gpt-4.1","input_tokens":5000,"output_tokens":500}}`)

	events1, _ := p.Parse(line)
	if events1 == nil {
		t.Fatal("first parse should return events")
	}

	events2, _ := p.Parse(line)
	if events2 != nil {
		t.Error("second parse should be deduped (nil)")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
