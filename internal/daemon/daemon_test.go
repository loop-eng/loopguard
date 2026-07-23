package daemon

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/loop-eng/loopguard/internal/analyzer"
	"github.com/loop-eng/loopguard/internal/api"
	"github.com/loop-eng/loopguard/internal/config"
	"github.com/loop-eng/loopguard/internal/discovery"
	"github.com/loop-eng/loopguard/internal/enforcer"
	"github.com/loop-eng/loopguard/internal/ltf"
	"github.com/loop-eng/loopguard/internal/notify"
	"github.com/loop-eng/loopguard/internal/parser"
	"github.com/loop-eng/loopguard/internal/watcher"
)

// newTestDaemon creates a minimal daemon for unit testing without starting
// the full Run() loop. It populates only the fields needed by the methods
// under test.
func newTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	logger := slog.Default()
	cfg := config.Default()
	budget := analyzer.NewBudgetEnforcer(100, 100, 100, 80)
	spinCfg := analyzer.SpinConfig{
		RepeatedCalls: 100, ErrorEcho: 100, StallMinutes: 100,
		CostVelocityPerMin: 100, ContextFillPercent: 85, WindowSize: 50,
	}
	tmpDir := t.TempDir()

	return &Daemon{
		logger:   logger,
		cfg:      cfg,
		ctx:      ctx,
		cancel:   cancel,
		registry: discovery.NewRegistry(),
		watcher:  watcher.New(logger),
		analyzer: analyzer.New(logger, budget, spinCfg, nil),
		enforcer: enforcer.New(logger, true),
		notifier: notify.New(logger, false, false),
		history:  NewHistory(logger, tmpDir+"/history.jsonl"),
		ltfEmitter: ltf.NewEmitter(logger, tmpDir, false),
		parsers: map[string]parser.Parser{
			"claude": parser.NewClaudeParser(),
		},
		paused: make(map[string]bool),
	}
}

func TestCheckPausedSessionsReapsDeadProcess(t *testing.T) {
	d := newTestDaemon(t)

	// Use a PID that definitely doesn't exist.
	deadPID := 999999999
	sessionID := "test-dead-session"

	d.registry.Add(&discovery.Session{
		ID:        sessionID,
		Agent:     "claude",
		PID:       deadPID,
		Active:    false,
		StartedAt: time.Now().Add(-time.Hour),
	})

	d.pausedMu.Lock()
	d.paused[sessionID] = true
	d.pausedMu.Unlock()

	// Run the reaper check.
	d.checkPausedSessions()

	// Assert: removed from paused map.
	d.pausedMu.RLock()
	stillPaused := d.paused[sessionID]
	d.pausedMu.RUnlock()
	if stillPaused {
		t.Error("session should have been removed from paused map")
	}

	// Assert: registry shows Terminated.
	session, ok := d.registry.Get(sessionID)
	if !ok {
		t.Fatal("session should still exist in registry")
	}
	if !session.Terminated {
		t.Error("session.Terminated should be true")
	}
}

func TestCheckPausedSessionsIgnoresAliveProcess(t *testing.T) {
	d := newTestDaemon(t)

	// Use our own PID (guaranteed alive and signalable).
	sessionID := "test-alive-session"
	alivePID := os.Getpid()

	d.registry.Add(&discovery.Session{
		ID:        sessionID,
		Agent:     "claude",
		PID:       alivePID,
		Active:    false,
		StartedAt: time.Now(),
	})

	d.pausedMu.Lock()
	d.paused[sessionID] = true
	d.pausedMu.Unlock()

	d.checkPausedSessions()

	// Assert: still paused (alive process).
	d.pausedMu.RLock()
	stillPaused := d.paused[sessionID]
	d.pausedMu.RUnlock()
	if !stillPaused {
		t.Error("session should still be in paused map (PID alive)")
	}

	session, _ := d.registry.Get(sessionID)
	if session.Terminated {
		t.Error("session.Terminated should be false for alive process")
	}
}

func TestResumeTerminatedSessionFails(t *testing.T) {
	d := newTestDaemon(t)

	sessionID := "test-terminated"
	d.registry.Add(&discovery.Session{
		ID:         sessionID,
		Agent:      "claude",
		PID:        999999999,
		Active:     false,
		Terminated: true,
		StartedAt:  time.Now(),
	})

	err := d.ResumeSession(context.Background(), sessionID)
	if err == nil {
		t.Fatal("expected error when resuming terminated session")
	}
	if !contains(err.Error(), "terminated") {
		t.Errorf("error should mention terminated, got: %v", err)
	}
}

func TestGetSessionReturnsDetail(t *testing.T) {
	d := newTestDaemon(t)

	sessionID := "test-detail"
	d.registry.Add(&discovery.Session{
		ID:         sessionID,
		Agent:      "codex",
		Path:       "/tmp/session.jsonl",
		ProjectDir: "/home/user/project",
		PID:        42,
		Active:     true,
		StartedAt:  time.Now(),
		LastEvent:  time.Now(),
	})

	detail, err := d.GetSession(sessionID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if detail.ID != sessionID {
		t.Errorf("ID = %q, want %q", detail.ID, sessionID)
	}
	if detail.PID != 42 {
		t.Errorf("PID = %d, want 42", detail.PID)
	}
	if detail.LogPath != "/tmp/session.jsonl" {
		t.Errorf("LogPath = %q, want /tmp/session.jsonl", detail.LogPath)
	}
}

func TestGetSessionNotFound(t *testing.T) {
	d := newTestDaemon(t)
	_, err := d.GetSession("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestGetConfigReturnsSnapshot(t *testing.T) {
	d := newTestDaemon(t)

	snap := d.GetConfig()
	if snap.Budget.PerSessionUSD != 20.0 {
		t.Errorf("Budget.PerSessionUSD = %v, want 20.0", snap.Budget.PerSessionUSD)
	}
	if snap.SpinDetection.ContextFillPercent != 85 {
		t.Errorf("SpinDetection.ContextFillPercent = %d, want 85", snap.SpinDetection.ContextFillPercent)
	}
}

// Satisfy the DaemonBackend interface compile check.
var _ api.DaemonBackend = (*Daemon)(nil)

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
