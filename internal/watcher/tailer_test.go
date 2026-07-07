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
