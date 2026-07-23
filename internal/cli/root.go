package cli

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"gopkg.in/lumberjack.v2"

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
	RunE:          runDaemon,
	SilenceErrors: true,
	SilenceUsage:  true,
}

// init finalizes the version string reported by --version. GoReleaser and
// Homebrew builds inject version/commit/date via -ldflags (see Makefile and
// .goreleaser.yaml) — the -X linker flag patches the string literals above
// before any Go code runs, so `version` already holds the real value here
// in that case. Plain `go install module@vX.Y.Z` builds don't support
// -ldflags, so version stays at its "dev" default; in that case, fall back
// to the Go module version embedded by the toolchain's build info.
//
// This must run in an init() rather than being computed inline in the
// rootCmd struct literal above: package-level variable initializers
// (including rootCmd's) all complete before any init() function runs, so
// setting rootCmd.Version as part of the literal would freeze in "dev"
// before this fallback ever had a chance to run.
func init() {
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok {
			if info.Main.Version != "" && info.Main.Version != "(devel)" {
				version = info.Main.Version
			}
		}
	}
	rootCmd.Version = version + " (" + commit + ") " + date
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

	d := daemon.New(ctx, logger, cfg, cfgPath)
	defer d.Shutdown()
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
			lj := &lumberjack.Logger{
				Filename:   cfg.Logging.File,
				MaxSize:    cfg.Logging.MaxSizeMB,
				MaxBackups: cfg.Logging.MaxBackups,
				MaxAge:     cfg.Logging.MaxAgeDays,
				Compress:   cfg.Logging.Compress,
				LocalTime:  true,
			}
			w = io.MultiWriter(os.Stderr, lj)
			cleanup = func() { _ = lj.Close() }
		}
	}

	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})), cleanup
}
