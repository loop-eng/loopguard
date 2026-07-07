package watcher

import (
	"bufio"
	"io"
	"os"
	"sync"
)

const maxLineSize = 1 << 20 // 1 MB — discard lines larger than this

type Tailer struct {
	mu     sync.Mutex
	path   string
	offset int64
	buf    []byte // partial line buffer
}

func NewTailer(path string) *Tailer {
	return &Tailer{path: path}
}

func (t *Tailer) SeekEnd() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	info, err := os.Stat(t.path)
	if err != nil {
		return err
	}
	t.offset = info.Size()
	return nil
}

func (t *Tailer) ReadNewLines() ([][]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

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

	// Prepend any buffered partial line
	var current []byte
	if len(t.buf) > 0 {
		current = append(current, t.buf...)
		t.buf = nil
	}

	for {
		chunk, err := reader.ReadBytes('\n')
		current = append(current, chunk...)

		if err == io.EOF {
			// Incomplete line — buffer it (capped)
			if len(current) > 0 && len(current) <= maxLineSize {
				t.buf = current
			} else {
				t.buf = nil // drop oversized partial
			}
			break
		}
		if err != nil {
			return lines, err
		}

		// Drop oversized lines to prevent OOM
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
