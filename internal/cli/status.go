package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show active agent sessions and their status",
	Long: `Queries the running LoopGuard daemon and displays all discovered sessions
with their current cost, rate, status, and duration.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := fetchStatus()
		if err != nil {
			fmt.Println("LoopGuard daemon is not running.")
			fmt.Println("Start it with: loopguard")
			return nil
		}

		if statusJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(status)
		}

		fmt.Println("LoopGuard — Active Sessions")
		fmt.Println()

		if len(status.Sessions) == 0 {
			fmt.Println("No sessions discovered.")
			return nil
		}

		fmt.Printf("%-10s %-8s %-25s %-10s %-8s %-10s\n",
			"ID", "Agent", "Project", "Cost", "Status", "Duration")

		for _, s := range status.Sessions {
			id := s.ID
			if len(id) > 8 {
				id = id[:8]
			}
			project := s.ProjectDir
			if len(project) > 24 {
				project = "..." + project[len(project)-21:]
			}

			status := "running"
			if s.Paused {
				status = "paused"
			} else if !s.Active {
				status = "done"
			}

			duration := time.Since(s.StartedAt).Truncate(time.Second)

			fmt.Printf("%-10s %-8s %-25s $%-9.2f %-8s %s\n",
				id, s.Agent, project, s.Cost, status, duration)
		}

		return nil
	},
}

var statusJSON bool

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "output as JSON")
	rootCmd.AddCommand(statusCmd)
}
