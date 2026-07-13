package notify

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type Urgency int

const (
	UrgencyNormal   Urgency = iota
	UrgencyCritical
)

type Notifier struct {
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

func (n *Notifier) Send(ctx context.Context, title, message string, urgency Urgency) error {
	if !n.enabled {
		return nil
	}
	n.logger.InfoContext(ctx, "notification", "title", title, "message", message)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	switch runtime.GOOS {
	case "darwin":
		return n.sendDarwin(ctx, title, message, urgency)
	case "linux":
		return n.sendLinux(ctx, title, message, urgency)
	default:
		n.logger.Warn("desktop notifications not supported on this platform")
		return nil
	}
}

func (n *Notifier) sendDarwin(ctx context.Context, title, message string, urgency Urgency) error {
	var soundClause string
	if n.sound {
		sound := "Glass"
		if urgency == UrgencyCritical {
			sound = "Basso"
		}
		soundClause = fmt.Sprintf(` sound name "%s"`, escapeAppleScript(sound))
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
