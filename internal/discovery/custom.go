package discovery

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var safeIDPattern = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

type CustomDiscoverer struct {
	logger *slog.Logger
	paths  []string
}

func NewCustomDiscoverer(logger *slog.Logger, paths []string) *CustomDiscoverer {
	return &CustomDiscoverer{
		logger: logger,
		paths:  paths,
	}
}

func (d *CustomDiscoverer) Agent() string { return "custom" }

func (d *CustomDiscoverer) BasePath() string {
	if len(d.paths) > 0 {
		return d.paths[0]
	}
	return ""
}

func (d *CustomDiscoverer) Discover(maxAge time.Duration) []*Session {
	cutoff := time.Now().Add(-maxAge)
	var sessions []*Session

	home, _ := os.UserHomeDir()

	for _, pattern := range d.paths {
		absPattern, err := filepath.Abs(pattern)
		if err != nil || (home != "" && !strings.HasPrefix(absPattern, home+string(filepath.Separator))) {
			d.logger.Warn("custom path outside home directory, skipping", "pattern", pattern)
			continue
		}

		matches, err := filepath.Glob(pattern)
		if err != nil {
			d.logger.Warn("invalid glob pattern", "pattern", pattern, "error", err)
			continue
		}

		for _, path := range matches {
			if !strings.HasSuffix(path, ".jsonl") {
				continue
			}
			info, err := os.Stat(path)
			if err != nil || info.IsDir() || info.ModTime().Before(cutoff) {
				continue
			}

			sessionID := safeIDPattern.ReplaceAllString(
				strings.TrimSuffix(filepath.Base(path), ".jsonl"), "_")
			sessions = append(sessions, &Session{
				ID:         sessionID,
				Agent:      "custom",
				Path:       path,
				ProjectDir: filepath.Dir(path),
				Active:     true,
				StartedAt:  info.ModTime(),
				LastEvent:  info.ModTime(),
			})
		}
	}

	d.logger.Debug("custom discovery complete", "sessions", len(sessions))
	return sessions
}
