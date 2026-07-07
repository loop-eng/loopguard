package cli

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/loop-eng/loopguard/internal/config"
	"github.com/loop-eng/loopguard/internal/daemon"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the LoopGuard daemon via service manager",
	Long:  "Starts LoopGuard as a background service using launchd (macOS) or systemd (Linux).",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load("")
		if err != nil {
			return err
		}

		if err := daemon.StartService(slog.Default(), cfg); err != nil {
			return fmt.Errorf("start failed: %w\nIs the service installed? Run: loopguard install", err)
		}

		fmt.Println("LoopGuard daemon started.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
}
