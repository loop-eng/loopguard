package discovery

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GeminiDiscoverer finds Gemini CLI session files under
// $GEMINI_DATA_DIR/tmp/<project_hash>/chats/ (or ~/.gemini/tmp/... by
// default). Gemini CLI sessions use two co-existing formats: a legacy
// monolithic JSON file rewritten in full on every turn (session-*.json),
// and a newer append-only JSONL file (session-*.jsonl). Both are
// discovered here; internal/parser.GeminiParser handles both formats.
type GeminiDiscoverer struct {
	logger  *slog.Logger
	baseDir string
}

func NewGeminiDiscoverer(logger *slog.Logger) *GeminiDiscoverer {
	return &GeminiDiscoverer{
		logger:  logger,
		baseDir: geminiBaseDir(),
	}
}

func (d *GeminiDiscoverer) Agent() string { return "gemini" }

func (d *GeminiDiscoverer) BasePath() string { return d.baseDir }

func (d *GeminiDiscoverer) Discover(maxAge time.Duration) []*Session {
	cutoff := time.Now().Add(-maxAge)

	projectDirs, err := os.ReadDir(d.baseDir)
	if err != nil {
		d.logger.Debug("gemini tmp dir not found", "path", d.baseDir, "error", err)
		return nil
	}

	// Track the best candidate file per session ID so that if both the
	// legacy .json and newer .jsonl form exist for the same session
	// (e.g. mid-migration), we register it once, preferring the most
	// recently modified file.
	type candidate struct {
		path       string
		projectDir string
		modTime    time.Time
	}
	bySessionID := make(map[string]candidate)

	for _, projDir := range projectDirs {
		if !projDir.IsDir() {
			continue
		}
		chatsDir := filepath.Join(d.baseDir, projDir.Name(), "chats")
		files, err := os.ReadDir(chatsDir)
		if err != nil {
			continue
		}

		for _, f := range files {
			if f.IsDir() {
				continue
			}
			name := f.Name()
			sessionID, ok := geminiSessionID(name)
			if !ok {
				continue
			}

			info, err := f.Info()
			if err != nil || info.ModTime().Before(cutoff) {
				continue
			}

			fullPath := filepath.Join(chatsDir, name)
			existing, seen := bySessionID[sessionID]
			if !seen || info.ModTime().After(existing.modTime) {
				bySessionID[sessionID] = candidate{
					path:       fullPath,
					projectDir: filepath.Join(d.baseDir, projDir.Name()),
					modTime:    info.ModTime(),
				}
			}
		}
	}

	sessions := make([]*Session, 0, len(bySessionID))
	for sessionID, c := range bySessionID {
		pid := findSessionPID(sessionID, c.path)
		sessions = append(sessions, &Session{
			ID:         sessionID,
			Agent:      "gemini",
			Path:       c.path,
			ProjectDir: c.projectDir,
			PID:        pid,
			Active:     pid > 0,
			StartedAt:  c.modTime,
			LastEvent:  c.modTime,
		})
	}

	d.logger.Info("gemini discovery complete", "sessions", len(sessions))
	return sessions
}

// geminiSessionID extracts the session ID from a Gemini chat filename,
// matching both "session-<id>.json" (legacy) and "session-<id>.jsonl"
// (current). Returns ok=false for anything else in the chats directory.
func geminiSessionID(filename string) (id string, ok bool) {
	const prefix = "session-"
	if !strings.HasPrefix(filename, prefix) {
		return "", false
	}
	switch {
	case strings.HasSuffix(filename, ".jsonl"):
		return strings.TrimSuffix(strings.TrimPrefix(filename, prefix), ".jsonl"), true
	case strings.HasSuffix(filename, ".json"):
		return strings.TrimSuffix(strings.TrimPrefix(filename, prefix), ".json"), true
	default:
		return "", false
	}
}

func geminiBaseDir() string {
	if dir := os.Getenv("GEMINI_DATA_DIR"); dir != "" {
		return filepath.Join(dir, "tmp")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gemini", "tmp")
}
