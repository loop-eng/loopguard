package cli

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/loop-eng/loopguard/internal/config"
	"github.com/loop-eng/loopguard/internal/daemon"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the LoopGuard daemon",
	Long:  "Stops the running LoopGuard daemon service.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load("")
		if err != nil {
			return err
		}

		if err := daemon.StopService(slog.Default(), cfg); err != nil {
			return fmt.Errorf("stop failed: %w", err)
		}

		fmt.Println("LoopGuard daemon stopped.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
