package discovery

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGeminiDiscovererAgent(t *testing.T) {
	d := NewGeminiDiscoverer(slog.Default())
	if d.Agent() != "gemini" {
		t.Errorf("Agent() = %v, want gemini", d.Agent())
	}
}

func TestGeminiDiscovererBasePathRespectsEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GEMINI_DATA_DIR", dir)

	d := NewGeminiDiscoverer(slog.Default())
	want := filepath.Join(dir, "tmp")
	if d.BasePath() != want {
		t.Errorf("BasePath() = %v, want %v", d.BasePath(), want)
	}
}

func TestGeminiDiscovererDiscoverJSONL(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GEMINI_DATA_DIR", dir)

	chatsDir := filepath.Join(dir, "tmp", "abc123", "chats")
	if err := os.MkdirAll(chatsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(chatsDir, "session-sess1.jsonl")
	if err := os.WriteFile(sessionFile, []byte(`{"type":"session_metadata"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := NewGeminiDiscoverer(slog.Default())
	sessions := d.Discover(24 * time.Hour)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != "sess1" {
		t.Errorf("ID = %v, want sess1", sessions[0].ID)
	}
	if sessions[0].Agent != "gemini" {
		t.Errorf("Agent = %v, want gemini", sessions[0].Agent)
	}
	if sessions[0].Path != sessionFile {
		t.Errorf("Path = %v, want %v", sessions[0].Path, sessionFile)
	}
}

func TestGeminiDiscovererDiscoverLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GEMINI_DATA_DIR", dir)

	chatsDir := filepath.Join(dir, "tmp", "hash1", "chats")
	if err := os.MkdirAll(chatsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(chatsDir, "session-legacy1.json")
	if err := os.WriteFile(sessionFile, []byte(`{"sessionId":"legacy1","messages":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	d := NewGeminiDiscoverer(slog.Default())
	sessions := d.Discover(24 * time.Hour)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != "legacy1" {
		t.Errorf("ID = %v, want legacy1", sessions[0].ID)
	}
}

func TestGeminiDiscovererIgnoresUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GEMINI_DATA_DIR", dir)

	chatsDir := filepath.Join(dir, "tmp", "hash1", "chats")
	if err := os.MkdirAll(chatsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chatsDir, "README.md"), []byte("not a session"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chatsDir, "other.txt"), []byte("not a session"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := NewGeminiDiscoverer(slog.Default())
	sessions := d.Discover(24 * time.Hour)
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestGeminiDiscovererPrefersNewerDuplicate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GEMINI_DATA_DIR", dir)

	chatsDir := filepath.Join(dir, "tmp", "hash1", "chats")
	if err := os.MkdirAll(chatsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	oldFile := filepath.Join(chatsDir, "session-dup.json")
	newFile := filepath.Join(chatsDir, "session-dup.jsonl")

	if err := os.WriteFile(oldFile, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldFile, old, old); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(newFile, []byte(`{"type":"session_metadata"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := NewGeminiDiscoverer(slog.Default())
	sessions := d.Discover(24 * time.Hour)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 deduplicated session, got %d", len(sessions))
	}
	if sessions[0].Path != newFile {
		t.Errorf("expected newer file %v to win, got %v", newFile, sessions[0].Path)
	}
}

func TestGeminiDiscovererMissingBaseDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GEMINI_DATA_DIR", filepath.Join(dir, "nonexistent"))

	d := NewGeminiDiscoverer(slog.Default())
	sessions := d.Discover(24 * time.Hour)
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions for missing base dir, got %d", len(sessions))
	}
}

func TestGeminiDiscovererRespectsMaxAge(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GEMINI_DATA_DIR", dir)

	chatsDir := filepath.Join(dir, "tmp", "hash1", "chats")
	if err := os.MkdirAll(chatsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(chatsDir, "session-old.jsonl")
	if err := os.WriteFile(sessionFile, []byte(`{}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(sessionFile, old, old); err != nil {
		t.Fatal(err)
	}

	d := NewGeminiDiscoverer(slog.Default())
	sessions := d.Discover(24 * time.Hour)
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions older than maxAge, got %d", len(sessions))
	}
}
