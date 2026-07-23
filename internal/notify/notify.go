package notify

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Urgency int

const (
	UrgencyNormal   Urgency = iota
	UrgencyCritical
)

type Notifier struct {
	mu      sync.RWMutex
	logger  *slog.Logger
	enabled bool
	sound   bool
}

func New(logger *slog.Logger, enabled, sound bool) *Notifier {
	return &Notifier{
		logger:  logger,
		enabled: enabled,
		sound:   sound,
	}
}

// UpdateSettings updates notification settings at runtime (config hot-reload).
func (n *Notifier) UpdateSettings(desktop, sound bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.enabled = desktop
	n.sound = sound
}

func (n *Notifier) Send(ctx context.Context, title, message string, urgency Urgency) error {
	n.mu.RLock()
	enabled := n.enabled
	sound := n.sound
	n.mu.RUnlock()

	if !enabled {
		return nil
	}
	n.logger.InfoContext(ctx, "notification", "title", title, "message", message)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	switch runtime.GOOS {
	case "darwin":
		return n.sendDarwin(ctx, title, message, urgency, sound)
	case "linux":
		return n.sendLinux(ctx, title, message, urgency)
	default:
		n.logger.Warn("desktop notifications not supported on this platform")
		return nil
	}
}

func (n *Notifier) sendDarwin(ctx context.Context, title, message string, urgency Urgency, playSound bool) error {
	var soundClause string
	if playSound {
		snd := "Glass"
		if urgency == UrgencyCritical {
			snd = "Basso"
		}
		soundClause = fmt.Sprintf(` sound name "%s"`, escapeAppleScript(snd))
	}
	script := fmt.Sprintf(
		`display notification "%s" with title "%s"%s`,
		escapeAppleScript(message), escapeAppleScript(title), soundClause,
	)
	return exec.CommandContext(ctx, "osascript", "-e", script).Run()
}

func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func (n *Notifier) sendLinux(ctx context.Context, title, message string, urgency Urgency) error {
	u := "normal"
	if urgency == UrgencyCritical {
		u = "critical"
	}
	return exec.CommandContext(ctx, "notify-send", "-u", u, title, message).Run()
}
