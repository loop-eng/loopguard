package discovery

import (
	"log/slog"
	"testing"
	"time"
)

func TestRegistryAddGet(t *testing.T) {
	r := NewRegistry()
	s := &Session{
		ID:        "test-123",
		Agent:     "claude",
		Path:      "/tmp/test.jsonl",
		Active:    true,
		StartedAt: time.Now(),
	}

	r.Add(s)

	got, ok := r.Get("test-123")
	if !ok {
		t.Fatal("session not found after Add")
	}
	if got.Agent != "claude" {
		t.Errorf("Agent = %v, want claude", got.Agent)
	}
}

func TestRegistryGetReturnsCopy(t *testing.T) {
	r := NewRegistry()
	r.Add(&Session{ID: "s1", Active: true})

	got, _ := r.Get("s1")
	got.Active = false

	original, _ := r.Get("s1")
	if !original.Active {
		t.Error("mutating returned copy should not affect registry")
	}
}

func TestRegistryUpdate(t *testing.T) {
	r := NewRegistry()
	r.Add(&Session{ID: "s1", Active: true})

	ok := r.Update("s1", func(s *Session) {
		s.Active = false
	})
	if !ok {
		t.Fatal("Update returned false for existing session")
	}

	got, _ := r.Get("s1")
	if got.Active {
		t.Error("Update should have set Active to false")
	}
}

func TestRegistryUpdateMissing(t *testing.T) {
	r := NewRegistry()
	ok := r.Update("missing", func(s *Session) {
		s.Active = false
	})
	if ok {
		t.Error("Update should return false for missing session")
	}
}

func TestRegistryRemove(t *testing.T) {
	r := NewRegistry()
	r.Add(&Session{ID: "s1", Active: true})
	r.Remove("s1")

	_, ok := r.Get("s1")
	if ok {
		t.Error("session found after Remove")
	}
}

func TestRegistryActive(t *testing.T) {
	r := NewRegistry()
	r.Add(&Session{ID: "active-1", Active: true})
	r.Add(&Session{ID: "active-2", Active: true})
	r.Add(&Session{ID: "inactive", Active: false})

	active := r.Active()
	if len(active) != 2 {
		t.Errorf("Active() returned %d sessions, want 2", len(active))
	}
}

func TestRegistryAll(t *testing.T) {
	r := NewRegistry()
	r.Add(&Session{ID: "s1"})
	r.Add(&Session{ID: "s2"})
	r.Add(&Session{ID: "s3"})

	all := r.All()
	if len(all) != 3 {
		t.Errorf("All() returned %d sessions, want 3", len(all))
	}
}

func TestCustomDiscoverer(t *testing.T) {
	d := NewCustomDiscoverer(slog.Default(), []string{"/nonexistent/*.jsonl"})
	if d.Agent() != "custom" {
		t.Errorf("Agent() = %v, want custom", d.Agent())
	}
	sessions := d.Discover(24 * time.Hour)
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions from nonexistent path, got %d", len(sessions))
	}
}

func TestCodexDiscovererAgent(t *testing.T) {
	d := NewCodexDiscoverer(slog.Default())
	if d.Agent() != "codex" {
		t.Errorf("Agent() = %v, want codex", d.Agent())
	}
}
