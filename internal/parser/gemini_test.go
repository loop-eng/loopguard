package parser

import (
	"testing"
)

func TestGeminiParserJSONLSessionMetadata(t *testing.T) {
	p := NewGeminiParser()
	line := []byte(`{"type":"session_metadata","sessionId":"sess-1","projectHash":"abc","startTime":"2026-07-10T10:00:00Z"}`)

	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if events != nil {
		t.Errorf("expected nil events for session_metadata, got %d", len(events))
	}
}

func TestGeminiParserJSONLTextMessage(t *testing.T) {
	p := NewGeminiParser()
	line := []byte(`{"type":"gemini","id":"msg2","content":[{"text":"Hi"}]}`)

	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ContentType != ContentText {
		t.Errorf("ContentType = %v, want ContentText", events[0].ContentType)
	}
}

func TestGeminiParserJSONLFunctionCall(t *testing.T) {
	p := NewGeminiParser()
	line := []byte(`{"type":"gemini","id":"msg3","content":[{"functionCall":{"name":"run_shell_command","args":{"command":"npm test"}}}]}`)

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
	if ev.ToolName != "run_shell_command" {
		t.Errorf("ToolName = %q, want run_shell_command", ev.ToolName)
	}
	if ev.ToolInput == "" {
		t.Error("expected ToolInput to be populated")
	}
}

func TestGeminiParserJSONLFunctionResponse(t *testing.T) {
	p := NewGeminiParser()
	line := []byte(`{"type":"user","id":"msg4","content":[{"functionResponse":{"name":"run_shell_command","response":"ok"}}]}`)

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
		t.Error("plain 'ok' response should not be treated as error")
	}
}

func TestGeminiParserJSONLFunctionResponseError(t *testing.T) {
	p := NewGeminiParser()
	line := []byte(`{"type":"user","id":"msg5","content":[{"functionResponse":{"name":"run_shell_command","response":{"error":"command not found"}}}]}`)

	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if !events[0].IsError {
		t.Error("expected IsError=true for response with error key")
	}
	if events[0].ErrorMsg == "" {
		t.Error("expected ErrorMsg to be populated")
	}
}

func TestGeminiParserJSONLMessageUpdate(t *testing.T) {
	p := NewGeminiParser()
	line := []byte(`{"type":"message_update","id":"msg2","tokens":{"input":10,"output":5}}`)

	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Tokens.InputTokens != 10 {
		t.Errorf("InputTokens = %d, want 10", events[0].Tokens.InputTokens)
	}
	if events[0].Tokens.OutputTokens != 5 {
		t.Errorf("OutputTokens = %d, want 5", events[0].Tokens.OutputTokens)
	}
}

func TestGeminiParserJSONLMessageUpdateDedup(t *testing.T) {
	p := NewGeminiParser()
	line := []byte(`{"type":"message_update","id":"msg2","tokens":{"input":10,"output":5}}`)

	events1, _ := p.Parse(line)
	if len(events1) != 1 {
		t.Fatalf("first parse: expected 1 event, got %d", len(events1))
	}

	events2, _ := p.Parse(line)
	if events2 != nil {
		t.Errorf("second parse of same id should be deduped (nil), got %d events", len(events2))
	}
}

func TestGeminiParserJSONLUnknownType(t *testing.T) {
	p := NewGeminiParser()
	line := []byte(`{"type":"future_event_type","id":"x"}`)

	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if events != nil {
		t.Errorf("expected nil events for unknown type, got %d", len(events))
	}
}

func TestGeminiParserLegacyConversation(t *testing.T) {
	p := NewGeminiParser()
	line := []byte(`{
		"sessionId": "legacy-1",
		"messages": [
			{"role": "user", "parts": [{"text": "Hello"}]},
			{"role": "model", "parts": [{"text": "Hi there"}], "usageMetadata": {"promptTokenCount": 1171, "candidatesTokenCount": 44, "totalTokenCount": 1298, "cachedContentTokenCount": 0, "thoughtsTokenCount": 83}, "modelVersion": "gemini-2.5-flash"}
		]
	}`)

	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].EntryType != "user" || events[0].ContentType != ContentText {
		t.Errorf("first event = %+v, want user/text", events[0])
	}

	second := events[1]
	if second.EntryType != "assistant" {
		t.Errorf("EntryType = %q, want assistant", second.EntryType)
	}
	if second.Model != "gemini-2.5-flash" {
		t.Errorf("Model = %q, want gemini-2.5-flash", second.Model)
	}
	if second.Tokens.InputTokens != 1171 {
		t.Errorf("InputTokens = %d, want 1171", second.Tokens.InputTokens)
	}
	// candidatesTokenCount (44) + thoughtsTokenCount (83) = 127, billed at output rate.
	if second.Tokens.OutputTokens != 127 {
		t.Errorf("OutputTokens = %d, want 127", second.Tokens.OutputTokens)
	}
	if second.Tokens.CacheReadTokens != 0 {
		t.Errorf("CacheReadTokens = %d, want 0", second.Tokens.CacheReadTokens)
	}
}

func TestGeminiParserLegacyConversationOnlyEmitsNewMessages(t *testing.T) {
	p := NewGeminiParser()

	line1 := []byte(`{"sessionId":"legacy-2","messages":[{"role":"user","parts":[{"text":"Hello"}]}]}`)
	events1, err := p.Parse(line1)
	if err != nil {
		t.Fatalf("first parse error: %v", err)
	}
	if len(events1) != 1 {
		t.Fatalf("expected 1 event on first parse, got %d", len(events1))
	}

	// Second parse: the file was rewritten with the full (growing) history —
	// only the new message should be emitted, not a duplicate of the first.
	line2 := []byte(`{"sessionId":"legacy-2","messages":[{"role":"user","parts":[{"text":"Hello"}]},{"role":"model","parts":[{"text":"Hi"}]}]}`)
	events2, err := p.Parse(line2)
	if err != nil {
		t.Fatalf("second parse error: %v", err)
	}
	if len(events2) != 1 {
		t.Fatalf("expected 1 new event on second parse, got %d", len(events2))
	}
	if events2[0].EntryType != "assistant" {
		t.Errorf("expected the new event to be the assistant reply, got %q", events2[0].EntryType)
	}

	// Third parse with the same content: nothing new should be emitted.
	events3, err := p.Parse(line2)
	if err != nil {
		t.Fatalf("third parse error: %v", err)
	}
	if len(events3) != 0 {
		t.Errorf("expected 0 events for unchanged conversation, got %d", len(events3))
	}
}

func TestGeminiParserLegacyConversationFunctionCall(t *testing.T) {
	p := NewGeminiParser()
	line := []byte(`{"sessionId":"legacy-3","messages":[{"role":"model","parts":[{"functionCall":{"name":"edit_file","args":{"path":"/a.go"}}}]}]}`)

	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ContentType != ContentToolUse {
		t.Errorf("ContentType = %v, want ContentToolUse", events[0].ContentType)
	}
	if events[0].ToolName != "edit_file" {
		t.Errorf("ToolName = %q, want edit_file", events[0].ToolName)
	}
}

func TestGeminiParserLegacyConversationFunctionResponseError(t *testing.T) {
	p := NewGeminiParser()
	line := []byte(`{"sessionId":"legacy-4","messages":[{"role":"user","parts":[{"functionResponse":{"name":"edit_file","response":"Error: file not found"}}]}]}`)

	events, err := p.Parse(line)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if !events[0].IsError {
		t.Error("expected IsError=true for response string containing 'Error'")
	}
}

func TestGeminiParserMalformedInput(t *testing.T) {
	p := NewGeminiParser()

	if _, err := p.Parse([]byte(`not json`)); err == nil {
		t.Error("expected error for invalid JSON")
	}
	if _, err := p.Parse([]byte(``)); err == nil {
		t.Error("expected error for empty input")
	}
}

func TestGeminiParserEmptyObject(t *testing.T) {
	p := NewGeminiParser()

	events, err := p.Parse([]byte(`{}`))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if events != nil {
		t.Errorf("expected nil events for empty object, got %d", len(events))
	}
}
