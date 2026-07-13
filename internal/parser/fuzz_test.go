package parser

import (
	"testing"
)

func FuzzClaudeParser(f *testing.F) {
	p := NewClaudeParser()

	f.Add([]byte(`{"type":"assistant","uuid":"u1","requestId":"r1","sessionId":"s1","timestamp":"2026-07-10T10:00:00Z","message":{"role":"assistant","model":"claude-opus-4-6","content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":1000,"output_tokens":100}}}`))
	f.Add([]byte(`{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-07-10T10:00:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok","is_error":false}]}}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"type":"assistant","message":{"content":[]}}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(``))
	f.Add([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"test"}}],"usage":{"input_tokens":0,"output_tokens":0}}}`))
	f.Add([]byte(`{"type":"user","message":{"content":[{"type":"tool_result","content":null,"is_error":true}]}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic on any input
		p.Parse(data)
	})
}

func FuzzCodexParser(f *testing.F) {
	p := NewCodexParser()

	f.Add([]byte(`{"type":"tool_call_started","id":"tc1","session_id":"s1","timestamp":"2026-07-10T10:00:00Z","data":{"name":"shell","input":"npm test"}}`))
	f.Add([]byte(`{"type":"inference_completed","id":"inf1","session_id":"s1","timestamp":"2026-07-10T10:00:00Z","data":{"model":"gpt-4.1","input_tokens":5000,"output_tokens":500}}`))
	f.Add([]byte(`{"type":"tool_call_ended","id":"tc1","data":{"output":"ok","is_error":false}}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(``))
	f.Add([]byte(`{"type":"tool_call_started","data":"not-an-object"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		p.Parse(data)
	})
}
