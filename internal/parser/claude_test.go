package parser

import (
	"testing"
)

func TestParseAssistantToolUse(t *testing.T) {
	line := []byte(`{"type":"assistant","uuid":"abc-123","requestId":"req-1","sessionId":"sess-1","timestamp":"2026-07-01T10:00:00Z","message":{"role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"tool_use","name":"Edit","id":"toolu_1","input":{"file_path":"/tmp/test.go","old_string":"foo","new_string":"bar"}}],"usage":{"input_tokens":5000,"output_tokens":200,"cache_read_input_tokens":3000,"cache_creation_input_tokens":1000}}}`)

	p := NewClaudeParser()
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
	if ev.ToolName != "Edit" {
		t.Errorf("ToolName = %v, want Edit", ev.ToolName)
	}
	if ev.Model != "claude-sonnet-4-6" {
		t.Errorf("Model = %v, want claude-sonnet-4-6", ev.Model)
	}
	if ev.Tokens.InputTokens != 5000 {
		t.Errorf("InputTokens = %d, want 5000", ev.Tokens.InputTokens)
	}
	if ev.Tokens.OutputTokens != 200 {
		t.Errorf("OutputTokens = %d, want 200", ev.Tokens.OutputTokens)
	}
	if len(ev.FilesChanged) != 1 || ev.FilesChanged[0] != "/tmp/test.go" {
		t.Errorf("FilesChanged = %v, want [/tmp/test.go]", ev.FilesChanged)
	}
}

func TestParseDeduplicateByRequestID(t *testing.T) {
	// Two lines with same requestId — tokens should only count on first
	line1 := []byte(`{"type":"assistant","uuid":"a1","requestId":"req-1","message":{"role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":1000,"output_tokens":50,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`)
	line2 := []byte(`{"type":"assistant","uuid":"a2","requestId":"req-1","message":{"role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"tool_use","name":"Read","input":{"file_path":"/tmp/x"}}],"usage":{"input_tokens":1000,"output_tokens":50,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`)

	p := NewClaudeParser()

	events1, _ := p.Parse(line1)
	events2, _ := p.Parse(line2)

	if len(events1) != 1 || events1[0].Tokens.InputTokens != 1000 {
		t.Errorf("first line should have tokens, got %d", events1[0].Tokens.InputTokens)
	}
	if len(events2) != 1 || events2[0].Tokens.InputTokens != 0 {
		t.Errorf("second line with same requestId should have 0 tokens, got %d", events2[0].Tokens.InputTokens)
	}
}

func TestParseToolResult(t *testing.T) {
	line := []byte(`{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"test output","is_error":false}]}}`)

	p := NewClaudeParser()
	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	ev := events[0]
	if ev.ContentType != ContentToolResult {
		t.Errorf("ContentType = %v, want ContentToolResult", ev.ContentType)
	}
	if ev.IsError {
		t.Error("should not be error")
	}
}

func TestParseToolResultError(t *testing.T) {
	line := []byte(`{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"command not found: npm","is_error":true}]}}`)

	p := NewClaudeParser()
	events, _ := p.Parse(line)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if !events[0].IsError {
		t.Error("should be error")
	}
	if events[0].ErrorMsg == "" {
		t.Error("ErrorMsg should be set")
	}
}

func TestParseSkipsIrrelevantTypes(t *testing.T) {
	lines := [][]byte{
		[]byte(`{"type":"queue-operation"}`),
		[]byte(`{"type":"ai-title"}`),
		[]byte(`{"type":"file-history-snapshot"}`),
		[]byte(`{"type":"last-prompt"}`),
		[]byte(`{"type":"attachment"}`),
	}

	p := NewClaudeParser()
	for _, line := range lines {
		events, err := p.Parse(line)
		if err != nil {
			t.Errorf("unexpected error for %s: %v", line, err)
		}
		if events != nil {
			t.Errorf("expected nil events for %s, got %d", line, len(events))
		}
	}
}

func TestIsTestCommand(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{`{"command":"npm test"}`, true},
		{`{"command":"go test ./..."}`, true},
		{`{"command":"pytest -v"}`, true},
		{`{"command":"cargo test"}`, true},
		{`{"command":"ls -la"}`, false},
		{`{"command":"git status"}`, false},
	}

	for _, tt := range tests {
		got := IsTestCommand(tt.input)
		if got != tt.want {
			t.Errorf("IsTestCommand(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsFileModifyingTool(t *testing.T) {
	if !IsFileModifyingTool("Edit") {
		t.Error("Edit should be file-modifying")
	}
	if !IsFileModifyingTool("Write") {
		t.Error("Write should be file-modifying")
	}
	if IsFileModifyingTool("Read") {
		t.Error("Read should not be file-modifying")
	}
	if IsFileModifyingTool("Bash") {
		t.Error("Bash should not be file-modifying")
	}
}
