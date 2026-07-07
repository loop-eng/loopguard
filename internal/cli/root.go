package cli

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/loop-eng/loopguard/internal/api"
	"github.com/loop-eng/loopguard/internal/config"
	"github.com/loop-eng/loopguard/internal/daemon"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "loopguard",
	Short: "Circuit breaker daemon for AI agent loops",
	Long: `LoopGuard monitors AI agent sessions (Claude Code, Codex, Gemini CLI) in real-time,
detects runaway loops, enforces budget limits, and pauses offending processes.

Run without arguments to start the daemon in the foreground.
Use 'loopguard install' to set up auto-start on login.`,
	Version:       version + " (" + commit + ") " + date,
	RunE:          runDaemon,
	SilenceErrors: true,
	SilenceUsage:  true,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringP("config", "c", "", "config file (default: ~/.config/loopguard/config.yaml)")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "enable debug logging")
}

func runDaemon(cmd *cobra.Command, args []string) error {
	if api.IsDaemonRunning() {
		cmd.Println("LoopGuard daemon is already running.")
		return nil
	}

	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	verbose, _ := cmd.Flags().GetBool("verbose")
	logger, logCleanup := setupLogger(cfg, verbose)
	defer logCleanup()

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd.Println("LoopGuard daemon starting...")

	d := daemon.New(ctx, logger, cfg)
	return d.Run()
}

func setupLogger(cfg *config.Config, verbose bool) (*slog.Logger, func()) {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.Logging.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	if verbose {
		level = slog.LevelDebug
	}

	var w io.Writer = os.Stderr
	cleanup := func() {}

	if cfg.Logging.File != "" {
		dir := filepath.Dir(cfg.Logging.File)
		os.MkdirAll(dir, 0700)
		if info, err := os.Lstat(cfg.Logging.File); err == nil && info.Mode()&os.ModeSymlink != 0 {
			slog.Warn("refusing to follow symlink for log file", "path", cfg.Logging.File)
		} else {
			f, err := os.OpenFile(cfg.Logging.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err == nil {
				w = io.MultiWriter(os.Stderr, f)
				cleanup = func() { f.Close() }
			}
		}
	}

	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})), cleanup
}
