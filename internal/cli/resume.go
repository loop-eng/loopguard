package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var resumeCmd = &cobra.Command{
	Use:   "resume <session-id>",
	Short: "Resume a paused agent session",
	Long: `Sends SIGCONT to a session that was paused by LoopGuard,
allowing the agent to continue from where it left off.

You can use the first 8 characters of the session ID.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sessionID := args[0]
		if sessionID == "" {
			return fmt.Errorf("session ID cannot be empty")
		}

		status, err := fetchStatus()
		if err != nil {
			return fmt.Errorf("daemon not running — cannot resume")
		}

		var matches []string
		for _, s := range status.Sessions {
			if len(sessionID) <= len(s.ID) && s.ID[:len(sessionID)] == sessionID {
				matches = append(matches, s.ID)
			}
		}

		switch len(matches) {
		case 0:
			return fmt.Errorf("no session matching prefix %q", sessionID)
		case 1:
			// unambiguous
		default:
			fmt.Printf("Ambiguous prefix %q matches %d sessions:\n", sessionID, len(matches))
			for _, m := range matches {
				fmt.Printf("  %s\n", m)
			}
			return fmt.Errorf("provide a longer prefix to disambiguate")
		}

		if err := resumeSession(matches[0]); err != nil {
			return fmt.Errorf("failed to resume: %w", err)
		}

		fmt.Printf("Session %s resumed.\n", matches[0][:8])
		return nil
	},
}

func init() {
	rootCmd.AddCommand(resumeCmd)
}
