package daemon

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type HistoryEntry struct {
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"session_id"`
	Agent     string    `json:"agent"`
	Action    string    `json:"action"`
	Trigger   string    `json:"trigger"`
	CostUSD   float64   `json:"cost_usd"`
}

type History struct {
	mu     sync.Mutex
	logger *slog.Logger
	path   string
}

func NewHistory(logger *slog.Logger, path string) *History {
	os.MkdirAll(filepath.Dir(path), 0700)
	return &History{logger: logger, path: path}
}

func (h *History) Record(sessionID, agent, action, trigger string, cost float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	entry := HistoryEntry{
		Timestamp: time.Now(),
		SessionID: sessionID,
		Agent:     agent,
		Action:    action,
		Trigger:   trigger,
		CostUSD:   cost,
	}

	if info, err := os.Lstat(h.path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		h.logger.Error("refusing to follow symlink for history file", "path", h.path)
		return
	}

	f, err := os.OpenFile(h.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		h.logger.Error("failed to open history file", "error", err)
		return
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(entry); err != nil {
		h.logger.Error("failed to write history", "error", err)
	}
}
