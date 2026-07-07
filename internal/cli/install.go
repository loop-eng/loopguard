package cli

import (
	"fmt"
	"log/slog"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/loop-eng/loopguard/internal/config"
	"github.com/loop-eng/loopguard/internal/daemon"
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install LoopGuard as a system service",
	Long: `Installs LoopGuard to start automatically on login.
  macOS: creates a launchd plist in ~/Library/LaunchAgents/
  Linux: creates a systemd user service`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load("")
		if err != nil {
			return err
		}
		logger := slog.Default()

		if err := daemon.InstallService(logger, cfg); err != nil {
			return fmt.Errorf("install failed: %w", err)
		}

		switch runtime.GOOS {
		case "darwin":
			fmt.Println("LoopGuard installed as launchd service (starts on login).")
			fmt.Println("Start now with: loopguard start")
		case "linux":
			fmt.Println("LoopGuard installed as systemd user service.")
			fmt.Println("Start now with: loopguard start")
		default:
			fmt.Println("LoopGuard service installed.")
		}
		return nil
	},
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove LoopGuard system service",
	Long:  "Removes the auto-start service created by 'loopguard install'.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load("")
		if err != nil {
			return err
		}

		if err := daemon.StopService(slog.Default(), cfg); err != nil {
			// Ignore stop errors — service might not be running
		}

		if err := daemon.UninstallService(slog.Default(), cfg); err != nil {
			return fmt.Errorf("uninstall failed: %w", err)
		}

		fmt.Println("LoopGuard service removed.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(uninstallCmd)
}
