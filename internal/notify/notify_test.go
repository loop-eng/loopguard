package notify

import (
	"context"
	"log/slog"
	"testing"
)

func TestNotifierDisabled(t *testing.T) {
	n := New(slog.Default(), false, false)
	err := n.Send(context.Background(), "test", "message", UrgencyNormal)
	if err != nil {
		t.Errorf("disabled notifier should return nil, got %v", err)
	}
}

func TestNewNotifier(t *testing.T) {
	n := New(slog.Default(), true, true)
	if n == nil {
		t.Fatal("New() returned nil")
	}
	if !n.enabled {
		t.Error("notifier should be enabled")
	}
	if !n.sound {
		t.Error("sound should be enabled")
	}
}
