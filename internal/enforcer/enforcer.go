package enforcer

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"syscall"
	"time"
)

type Action int

const (
	ActionWarn  Action = iota
	ActionPause
	ActionKill
)

func (a Action) String() string {
	switch a {
	case ActionWarn:
		return "warn"
	case ActionPause:
		return "pause"
	case ActionKill:
		return "kill"
	default:
		return "unknown"
	}
}

type Enforcer struct {
	logger           *slog.Logger
	sentinelFallback bool
}

func New(logger *slog.Logger, sentinelFallback bool) *Enforcer {
	return &Enforcer{
		logger:           logger,
		sentinelFallback: sentinelFallback,
	}
}

func (e *Enforcer) Execute(ctx context.Context, action Action, pid int, projectDir, reason string) error {
	e.logger.WarnContext(ctx, "enforcing action",
		"action", action.String(),
		"pid", pid,
		"reason", reason,
	)

	switch action {
	case ActionWarn:
		return nil
	case ActionPause:
		return e.pause(pid, projectDir)
	case ActionKill:
		return e.kill(ctx, pid)
	}
	return nil
}

func (e *Enforcer) Resume(ctx context.Context, pid int, projectDir string) error {
	if err := validatePID(pid); err != nil {
		return fmt.Errorf("cannot resume: %w", err)
	}

	if err := contProcess(pid); err != nil {
		return fmt.Errorf("SIGCONT failed: %w", err)
	}

	removeSentinel(projectDir)

	e.logger.InfoContext(ctx, "resumed agent", "pid", pid)
	return nil
}

func (e *Enforcer) pause(pid int, projectDir string) error {
	if err := validatePID(pid); err != nil {
		if e.sentinelFallback && projectDir != "" {
			e.logger.Warn("PID validation failed, using sentinel fallback", "error", err)
			return writeSentinel(projectDir)
		}
		return fmt.Errorf("cannot pause: %w", err)
	}

	if err := stopProcess(pid); err != nil {
		if e.sentinelFallback && projectDir != "" {
			e.logger.Warn("SIGSTOP failed, using sentinel fallback", "error", err)
			return writeSentinel(projectDir)
		}
		return fmt.Errorf("SIGSTOP failed: %w", err)
	}

	e.logger.Warn("paused agent via SIGSTOP", "pid", pid)
	return nil
}

func (e *Enforcer) kill(ctx context.Context, pid int) error {
	if err := validatePID(pid); err != nil {
		return fmt.Errorf("cannot kill: %w", err)
	}

	if err := termProcess(pid); err != nil {
		return fmt.Errorf("SIGTERM failed: %w", err)
	}

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
			if err := validatePID(pid); err == nil {
				if err := killProcess(pid); err != nil {
					e.logger.Error("SIGKILL failed", "pid", pid, "error", err)
				}
			}
		}
	}()

	return nil
}

func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
