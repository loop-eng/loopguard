package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var killCmd = &cobra.Command{
	Use:   "kill <session-id>",
	Short: "Kill a running agent session",
	Long: `Sends SIGTERM (escalating to SIGKILL after 5s) to terminate a session.

Unlike 'resume' (which simply unpauses), 'kill' terminates the agent process.
You can use the first 8 characters of the session ID.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sessionID := args[0]
		if sessionID == "" {
			return fmt.Errorf("session ID cannot be empty")
		}

		status, err := fetchStatus()
		if err != nil {
			return fmt.Errorf("daemon not running — cannot kill")
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

		if err := killSession(matches[0]); err != nil {
			return fmt.Errorf("failed to kill: %w", err)
		}

		id := matches[0]
		if len(id) > 8 {
			id = id[:8]
		}
		fmt.Printf("Session %s killed.\n", id)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(killCmd)
}
