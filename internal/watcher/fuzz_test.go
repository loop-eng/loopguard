package watcher

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzTailerReadNewLines(f *testing.F) {
	f.Add([]byte(`{"line":1}` + "\n"))
	f.Add([]byte(`{"line":1}` + "\n" + `{"line":2}` + "\n"))
	f.Add([]byte(`partial line no newline`))
	f.Add([]byte("\n\n\n"))
	f.Add([]byte{})
	f.Add([]byte(`{"a":"` + string(make([]byte, 100)) + `"}` + "\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.jsonl")
		if err := os.WriteFile(path, data, 0644); err != nil {
			return
		}

		tailer := NewTailer(path)
		// Must not panic
		tailer.ReadNewLines()

		// Append more data and read again
		f2, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return
		}
		f2.Write(data)
		f2.Close()

		tailer.ReadNewLines()
	})
}

// FuzzTailerWholeFileMode exercises the whole-file rewrite path used for
// legacy Gemini CLI ".json" session files (monolithic JSON, rewritten in
// full on every turn rather than appended to).
func FuzzTailerWholeFileMode(f *testing.F) {
	f.Add([]byte(`{"messages":[]}`))
	f.Add([]byte(`{"messages":[{"role":"user"}]}`))
	f.Add([]byte(`not json at all`))
	f.Add([]byte{})
	f.Add([]byte(`{"a":"` + string(make([]byte, 100)) + `"}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		path := filepath.Join(dir, "session-fuzz.json")
		if err := os.WriteFile(path, data, 0644); err != nil {
			return
		}

		tailer := NewTailer(path)
		// Must not panic on the initial whole-file read.
		tailer.ReadNewLines()

		// Simulate a full rewrite (legacy format semantics — not an
		// append) with different content and read again.
		rewritten := append([]byte("x"), data...)
		if err := os.WriteFile(path, rewritten, 0644); err != nil {
			return
		}
		tailer.ReadNewLines()
	})
}
