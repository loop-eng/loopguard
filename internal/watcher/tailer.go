package watcher

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

const maxLineSize = 1 << 20 // 1 MB — discard lines larger than this

// maxWholeFileSize bounds whole-file reads (see wholeFile mode below).
// Legacy Gemini CLI conversation files are monolithic JSON rewritten in
// full on every turn, so they can grow much larger than a single JSONL
// line; 50 MB is a generous ceiling that still prevents unbounded memory
// use from a runaway or corrupted file.
const maxWholeFileSize = 50 << 20

type Tailer struct {
	mu            sync.Mutex
	path          string
	offset        int64
	buf           []byte
	skipToNewline bool

	// wholeFile is true for paths that are rewritten in full on every
	// change rather than appended to (e.g. legacy Gemini CLI
	// "session-*.json" conversation files). For these, byte-offset tailing
	// is incorrect — instead the entire file is re-read on each change and
	// returned as a single chunk when its content differs from the last
	// read.
	wholeFile bool
	lastHash  [32]byte
	hasHash   bool
}

// NewTailer creates a tailer for path. Files ending in ".json" (but not
// ".jsonl") are treated as whole-file-rewrite sources; everything else is
// tailed by byte offset as an append-only line stream.
func NewTailer(path string) *Tailer {
	return &Tailer{
		path:      path,
		wholeFile: strings.HasSuffix(path, ".json"),
	}
}

func (t *Tailer) SeekEnd() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.wholeFile {
		data, err := os.ReadFile(t.path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		t.lastHash = sha256.Sum256(data)
		t.hasHash = true
		return nil
	}

	info, err := os.Stat(t.path)
	if err != nil {
		return err
	}
	t.offset = info.Size()
	return nil
}

// readWholeFile re-reads path in full and returns its content as a single
// element slice, but only when the content differs from the last read
// (tracked via a content hash) — this avoids re-emitting identical data on
// every poll/fsnotify tick for a file that hasn't actually changed.
func (t *Tailer) readWholeFile() ([][]byte, error) {
	data, err := os.ReadFile(t.path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	if len(data) > maxWholeFileSize {
		return nil, fmt.Errorf("file exceeds whole-file read limit: %d bytes > %d", len(data), maxWholeFileSize)
	}

	sum := sha256.Sum256(data)
	if t.hasHash && sum == t.lastHash {
		return nil, nil
	}
	t.lastHash = sum
	t.hasHash = true

	return [][]byte{data}, nil
}

func (t *Tailer) ReadNewLines() ([][]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.wholeFile {
		return t.readWholeFile()
	}

	f, err := os.Open(t.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	// Handle file truncation (e.g., log rotation)
	if info.Size() < t.offset {
		t.offset = 0
		t.buf = nil
	}

	if info.Size() == t.offset && len(t.buf) == 0 {
		return nil, nil
	}

	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(f)
	var lines [][]byte

	var current []byte
	if len(t.buf) > 0 {
		current = append(current, t.buf...)
		t.buf = nil
	}

	for {
		chunk, err := reader.ReadBytes('\n')
		current = append(current, chunk...)

		if err == io.EOF {
			if len(current) > 0 && len(current) <= maxLineSize {
				t.buf = current
			} else if len(current) > maxLineSize {
				t.buf = nil
				t.skipToNewline = true
			}
			break
		}
		if err != nil {
			return lines, err
		}

		if t.skipToNewline {
			t.skipToNewline = false
			current = current[:0]
			continue
		}

		if len(current) > maxLineSize {
			current = current[:0]
			continue
		}

		line := make([]byte, len(current))
		copy(line, current)
		lines = append(lines, line)
		current = current[:0]
	}

	// Update offset to current file position
	pos, err := f.Seek(0, io.SeekCurrent)
	if err == nil {
		t.offset = pos
	}

	return lines, nil
}

func (t *Tailer) Offset() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.offset
}
