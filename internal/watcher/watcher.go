package watcher

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Event struct {
	SessionID string
	Path      string
	Lines     [][]byte
}

type Watcher struct {
	logger       *slog.Logger
	events       chan Event
	done         chan struct{}
	closeOnce    sync.Once
	debouncer    *Debouncer
	tailers      map[string]*Tailer
	sessionIDs   map[string]string // path → explicit session ID
	mu           sync.Mutex
	pollInterval time.Duration
}

func New(logger *slog.Logger) *Watcher {
	return &Watcher{
		logger:       logger,
		events:       make(chan Event, 256),
		done:         make(chan struct{}),
		debouncer:    NewDebouncer(100 * time.Millisecond),
		tailers:      make(map[string]*Tailer),
		sessionIDs:   make(map[string]string),
		pollInterval: 5 * time.Second,
	}
}

func (w *Watcher) Events() <-chan Event {
	return w.events
}

func (w *Watcher) Watch(ctx context.Context, basePath string) error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		w.logger.Warn("fsnotify unavailable, using polling only", "error", err)
		return w.pollLoop(ctx, basePath)
	}
	defer fsw.Close()

	if err := w.addWatchRecursive(fsw, basePath); err != nil {
		w.logger.Warn("failed to add watches, using polling only", "error", err)
		return w.pollLoop(ctx, basePath)
	}

	w.logger.Info("watching directory", "path", basePath)

	pollTicker := time.NewTicker(w.pollInterval)
	defer pollTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case event, ok := <-fsw.Events:
			if !ok {
				return nil
			}
			if !w.isRelevant(event) {
				continue
			}

			if event.Op&fsnotify.Create != 0 {
				info, err := os.Lstat(event.Name)
				if err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
					fsw.Add(event.Name)
					continue
				}
			}

			path := event.Name
			w.debouncer.Trigger(path, func() {
				w.readAndEmit(path)
			})

		case err, ok := <-fsw.Errors:
			if !ok {
				return nil
			}
			w.logger.Warn("fsnotify error", "error", err)

		case <-pollTicker.C:
			w.pollAll(basePath)
		}
	}
}

func (w *Watcher) AddFile(path, sessionID string, seekEnd bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, exists := w.tailers[path]; exists {
		return
	}

	t := NewTailer(path)
	if seekEnd {
		t.SeekEnd()
	}
	w.tailers[path] = t
	if sessionID != "" {
		w.sessionIDs[path] = sessionID
	}
}

func (w *Watcher) RemoveFile(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.tailers, path)
	delete(w.sessionIDs, path)
}

func (w *Watcher) Close() error {
	w.closeOnce.Do(func() {
		close(w.done)
	})
	w.debouncer.Stop()
	return nil
}

func (w *Watcher) addWatchRecursive(fsw *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if err := fsw.Add(path); err != nil {
				w.logger.Debug("failed to watch dir", "path", path, "error", err)
			}
		}
		return nil
	})
}

func (w *Watcher) isRelevant(event fsnotify.Event) bool {
	if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
		return false
	}
	return strings.HasSuffix(event.Name, ".jsonl")
}

func (w *Watcher) readAndEmit(path string) {
	// Check done before any work
	select {
	case <-w.done:
		return
	default:
	}

	w.mu.Lock()
	tailer, exists := w.tailers[path]
	w.mu.Unlock()

	if !exists {
		tailer = NewTailer(path)
		w.mu.Lock()
		w.tailers[path] = tailer
		w.mu.Unlock()
	}

	lines, err := tailer.ReadNewLines()
	if err != nil {
		if os.IsNotExist(err) {
			w.mu.Lock()
			delete(w.tailers, path)
			delete(w.sessionIDs, path)
			w.mu.Unlock()
			w.logger.Info("removed tailer for deleted file", "path", path)
			return
		}
		w.logger.Warn("failed to read file", "path", path, "error", err)
		return
	}

	if len(lines) == 0 {
		return
	}

	w.mu.Lock()
	sessionID, ok := w.sessionIDs[path]
	w.mu.Unlock()
	if !ok {
		sessionID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}

	select {
	case w.events <- Event{
		SessionID: sessionID,
		Path:      path,
		Lines:     lines,
	}:
	case <-w.done:
		return
	}
}

func (w *Watcher) pollAll(basePath string) {
	w.mu.Lock()
	paths := make([]string, 0, len(w.tailers))
	for p := range w.tailers {
		paths = append(paths, p)
	}
	w.mu.Unlock()

	for _, path := range paths {
		w.readAndEmit(path)
	}
}

func (w *Watcher) pollLoop(ctx context.Context, basePath string) error {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.pollAll(basePath)
		}
	}
}
