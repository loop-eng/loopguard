package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/loop-eng/loopguard/internal/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage LoopGuard configuration",
	Long: `Opens or creates the LoopGuard configuration file.
Default location: ~/.config/loopguard/config.yaml`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := config.DefaultPath()
		if _, err := os.Stat(path); os.IsNotExist(err) {
			fmt.Printf("No config file found. Create one with: loopguard config init\n")
			fmt.Printf("Location: %s\n", path)
			return nil
		}
		fmt.Printf("Config file: %s\n", path)
		return nil
	},
}

const defaultConfigYAML = `# LoopGuard configuration
# All values shown are defaults — remove or modify as needed.

budget:
  per_session_usd: 20.0    # pause session after this cost
  per_hour_usd: 50.0       # hourly cap across all sessions
  per_day_usd: 200.0       # daily cap
  warn_at_percent: 80      # warn at this % of any limit

spin_detection:
  repeated_calls: 3         # same tool call N times → spin
  error_echo: 3             # same error N times → spin
  stall_minutes: 10         # no file changes for N min → warn
  cost_velocity_per_min: 2.0

enforcement:
  action: pause             # pause | kill | warn
  sentinel_fallback: true   # write .loopguard-stop if SIGSTOP fails

notifications:
  desktop: true
  sound: true

sources:
  claude_code: auto         # auto | disabled
  codex: auto               # auto | disabled
  gemini: auto              # auto | disabled
  custom: []                # additional glob patterns to watch

# Environment variable overrides:
#   LOOPGUARD_BUDGET_PER_SESSION=30
#   LOOPGUARD_BUDGET_PER_HOUR=100
#   LOOPGUARD_BUDGET_PER_DAY=500
#   LOOPGUARD_LOG_LEVEL=debug
`

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a default configuration file",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := config.DefaultPath()

		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("creating config dir: %w", err)
		}

		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			if os.IsExist(err) {
				return fmt.Errorf("config already exists at %s", path)
			}
			return fmt.Errorf("creating config: %w", err)
		}
		defer f.Close()

		if _, err := f.WriteString(defaultConfigYAML); err != nil {
			return fmt.Errorf("writing config: %w", err)
		}

		fmt.Printf("Config created at %s\n", path)
		return nil
	},
}

func init() {
	configCmd.AddCommand(configInitCmd)
	rootCmd.AddCommand(configCmd)
}
