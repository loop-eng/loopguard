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

		// Try to find full ID via status
		status, err := fetchStatus()
		if err != nil {
			return fmt.Errorf("daemon not running — cannot resume")
		}

		fullID := sessionID
		for _, s := range status.Sessions {
			if len(sessionID) <= len(s.ID) && s.ID[:len(sessionID)] == sessionID {
				fullID = s.ID
				break
			}
		}

		if err := resumeSession(fullID); err != nil {
			return fmt.Errorf("failed to resume: %w", err)
		}

		fmt.Printf("Session %s resumed.\n", sessionID)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(resumeCmd)
}
