package watcher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTailerReadNewLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	// Write initial content
	os.WriteFile(path, []byte(`{"type":"user"}`+"\n"), 0644)

	tailer := NewTailer(path)

	// First read gets the line
	lines, err := tailer.ReadNewLines()
	if err != nil {
		t.Fatalf("ReadNewLines error: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	// Second read with no new data
	lines, err = tailer.ReadNewLines()
	if err != nil {
		t.Fatalf("ReadNewLines error: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("expected 0 lines, got %d", len(lines))
	}

	// Append more data
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(`{"type":"assistant"}` + "\n")
	f.WriteString(`{"type":"system"}` + "\n")
	f.Close()

	lines, err = tailer.ReadNewLines()
	if err != nil {
		t.Fatalf("ReadNewLines error: %v", err)
	}
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(lines))
	}
}

func TestTailerSeekEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0644)

	tailer := NewTailer(path)
	tailer.SeekEnd()

	// Should get nothing — started at end
	lines, _ := tailer.ReadNewLines()
	if len(lines) != 0 {
		t.Errorf("expected 0 lines after SeekEnd, got %d", len(lines))
	}

	// Append new data
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString("line4\n")
	f.Close()

	lines, _ = tailer.ReadNewLines()
	if len(lines) != 1 {
		t.Errorf("expected 1 new line, got %d", len(lines))
	}
}

func TestTailerPartialLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	// Write a partial line (no trailing newline)
	os.WriteFile(path, []byte(`{"partial":true`), 0644)

	tailer := NewTailer(path)

	lines, _ := tailer.ReadNewLines()
	if len(lines) != 0 {
		t.Errorf("partial line should not be returned, got %d lines", len(lines))
	}

	// Complete the line
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString("}\n")
	f.Close()

	lines, _ = tailer.ReadNewLines()
	if len(lines) != 1 {
		t.Fatalf("expected 1 complete line, got %d", len(lines))
	}
	if string(lines[0]) != `{"partial":true}`+"\n" {
		t.Errorf("unexpected content: %q", string(lines[0]))
	}
}

func TestTailerWholeFileModeDetection(t *testing.T) {
	if !NewTailer("/tmp/session-abc.json").wholeFile {
		t.Error("expected .json path to use whole-file mode")
	}
	if NewTailer("/tmp/session-abc.jsonl").wholeFile {
		t.Error("expected .jsonl path to NOT use whole-file mode")
	}
}

func TestTailerWholeFileModeBasic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session-legacy.json")

	os.WriteFile(path, []byte(`{"messages":[{"role":"user"}]}`), 0644)

	tailer := NewTailer(path)

	lines, err := tailer.ReadNewLines()
	if err != nil {
		t.Fatalf("ReadNewLines error: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 whole-file chunk, got %d", len(lines))
	}
	if string(lines[0]) != `{"messages":[{"role":"user"}]}` {
		t.Errorf("unexpected content: %q", string(lines[0]))
	}

	// Re-reading with no change should produce nothing (dedup via hash).
	lines, err = tailer.ReadNewLines()
	if err != nil {
		t.Fatalf("ReadNewLines error: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("expected 0 chunks when unchanged, got %d", len(lines))
	}

	// Full rewrite with a GROWING conversation (legacy format semantics —
	// the whole file is replaced, not appended to).
	os.WriteFile(path, []byte(`{"messages":[{"role":"user"},{"role":"model"}]}`), 0644)
	lines, err = tailer.ReadNewLines()
	if err != nil {
		t.Fatalf("ReadNewLines error: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 chunk after rewrite, got %d", len(lines))
	}
	if string(lines[0]) != `{"messages":[{"role":"user"},{"role":"model"}]}` {
		t.Errorf("unexpected content after rewrite: %q", string(lines[0]))
	}
}

func TestTailerWholeFileModeSeekEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session-legacy.json")

	os.WriteFile(path, []byte(`{"messages":[{"role":"user"}]}`), 0644)

	tailer := NewTailer(path)
	if err := tailer.SeekEnd(); err != nil {
		t.Fatalf("SeekEnd error: %v", err)
	}

	// Content unchanged since SeekEnd — should read nothing.
	lines, _ := tailer.ReadNewLines()
	if len(lines) != 0 {
		t.Errorf("expected 0 chunks after SeekEnd with no change, got %d", len(lines))
	}

	// Rewrite the file — should now see the new content.
	os.WriteFile(path, []byte(`{"messages":[{"role":"user"},{"role":"model"}]}`), 0644)
	lines, _ = tailer.ReadNewLines()
	if len(lines) != 1 {
		t.Errorf("expected 1 chunk after rewrite following SeekEnd, got %d", len(lines))
	}
}

func TestTailerWholeFileModeEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session-empty.json")
	os.WriteFile(path, []byte(``), 0644)

	tailer := NewTailer(path)
	lines, err := tailer.ReadNewLines()
	if err != nil {
		t.Fatalf("ReadNewLines error: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("expected 0 chunks for empty file, got %d", len(lines))
	}
}

func TestTailerWholeFileModeOversized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session-huge.json")

	// Write a file just over the whole-file size limit.
	huge := make([]byte, maxWholeFileSize+1)
	os.WriteFile(path, huge, 0644)

	tailer := NewTailer(path)
	_, err := tailer.ReadNewLines()
	if err == nil {
		t.Error("expected error for file exceeding whole-file size limit")
	}
}

func TestTailerHandleTruncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0644)

	tailer := NewTailer(path)
	tailer.ReadNewLines() // consume all

	// Truncate file (simulate log rotation)
	os.WriteFile(path, []byte("new1\n"), 0644)

	lines, _ := tailer.ReadNewLines()
	if len(lines) != 1 {
		t.Errorf("expected 1 line after truncation, got %d", len(lines))
	}
}
