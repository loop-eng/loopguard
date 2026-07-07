package ltf

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Emitter struct {
	mu        sync.Mutex
	logger    *slog.Logger
	outputDir string
	enabled   bool
	writers   map[string]*sessionWriter
}

type sessionWriter struct {
	mu        sync.Mutex
	writer    *Writer
	file      *os.File
	startedAt time.Time
}

func NewEmitter(logger *slog.Logger, outputDir string, enabled bool) *Emitter {
	if enabled {
		if err := os.MkdirAll(outputDir, 0700); err != nil {
			logger.Error("failed to create LTF output dir", "path", outputDir, "error", err)
		}
	}
	return &Emitter{
		logger:    logger,
		outputDir: outputDir,
		enabled:   enabled,
		writers:   make(map[string]*sessionWriter),
	}
}

func (e *Emitter) EmitIntervention(sessionID, agent, action, trigger string, cost float64) {
	if !e.enabled {
		return
	}

	sw, err := e.getWriter(sessionID)
	if err != nil {
		e.logger.Error("failed to open LTF trace file", "session", sessionID, "error", err)
		return
	}

	phase := "error"
	if action == "paused" || action == "killed" {
		phase = "terminate"
	}

	event := Event{
		LoopID:    sessionID,
		SessionID: sessionID,
		Phase:     phase,
		Agent: &AgentInfo{
			Name:     agent,
			Provider: agentProvider(agent),
		},
		Action: &EventAction{
			Type:   "circuit_breaker",
			Detail: trigger,
		},
		CostUSD: cost,
		Result: &EventResult{
			Status: action,
			Detail: trigger,
		},
		Metadata: map[string]any{
			"source":  "loopguard",
			"action":  action,
			"trigger": trigger,
		},
	}

	sw.mu.Lock()
	err = sw.writer.WriteEvent(event)
	sw.mu.Unlock()
	if err != nil {
		e.logger.Error("failed to write LTF event", "session", sessionID, "error", err)
	}
}

func (e *Emitter) EmitSessionEnd(sessionID, agent, reason string, cost float64, startedAt time.Time) {
	if !e.enabled {
		return
	}

	sw, err := e.getWriter(sessionID)
	if err != nil {
		return
	}

	summary := LoopSummary{
		LoopID:            sessionID,
		SessionID:         sessionID,
		StartedAt:         startedAt,
		EndedAt:           time.Now(),
		TotalCostUSD:      cost,
		TotalDurationMs:   time.Since(startedAt).Milliseconds(),
		TerminationReason: reason,
		Metadata: map[string]any{
			"source": "loopguard",
			"agent":  agent,
		},
	}

	sw.mu.Lock()
	err = sw.writer.WriteSummary(summary)
	sw.mu.Unlock()
	if err != nil {
		e.logger.Error("failed to write LTF summary", "session", sessionID, "error", err)
	}

	e.closeWriter(sessionID)
}

func (e *Emitter) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, sw := range e.writers {
		sw.file.Close()
		delete(e.writers, id)
	}
}

func (e *Emitter) getWriter(sessionID string) (*sessionWriter, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if sw, ok := e.writers[sessionID]; ok {
		return sw, nil
	}

	path := filepath.Join(e.outputDir, sessionID+".ltf.jsonl")
	absPath, _ := filepath.Abs(path)
	absDir, _ := filepath.Abs(e.outputDir)
	if !strings.HasPrefix(absPath, absDir+string(os.PathSeparator)) {
		return nil, fmt.Errorf("invalid session ID: path escapes output directory")
	}

	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing to follow symlink: %s", path)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}

	sw := &sessionWriter{
		writer:    NewWriter(f),
		file:      f,
		startedAt: time.Now(),
	}
	e.writers[sessionID] = sw
	return sw, nil
}

func (e *Emitter) closeWriter(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if sw, ok := e.writers[sessionID]; ok {
		sw.file.Close()
		delete(e.writers, sessionID)
	}
}

func agentProvider(agent string) string {
	switch agent {
	case "claude":
		return "anthropic"
	case "codex":
		return "openai"
	case "gemini":
		return "google"
	default:
		return "unknown"
	}
}
