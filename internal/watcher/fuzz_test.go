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
