package cli

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/anunay999/vector/internal/stats"
	"github.com/spf13/cobra"
)

func newSpendCmd() *cobra.Command {
	var since time.Duration
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "spend",
		Short: "Summarize routed requests and estimated spend",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			snap, err := stats.Collect(cfg, since, 0, stats.Filter{})
			if err != nil {
				return err
			}

			if asJSON {
				fmt.Printf("{\"since\":%q,\"requests\":%d,\"tokens\":%d,\"estimated_cost_usd\":%.6f,\"off_plan_pct\":%.1f}\n",
					since.String(), snap.Requests, snap.Tokens(), snap.Cost, snap.OffPlanPct())
				return nil
			}

			fmt.Printf("Since %s: %d requests, %d tokens, est. $%.4f\n",
				snap.Since.Format("2006-01-02 15:04"), snap.Requests, snap.Tokens(), snap.Cost)
			if snap.Requests > 0 {
				fmt.Printf("Off-plan (non-subscription) share: %.0f%%\n", snap.OffPlanPct())
			}
			printGroups("By provider", snap.ByProvider)
			printGroups("By role", snap.ByRole)
			printGroups("By model", snap.ByModel)
			return nil
		},
	}
	cmd.Flags().DurationVar(&since, "since", 24*time.Hour, "window (e.g. 24h, 168h)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}

func printGroups(title string, m map[string]*stats.Group) {
	if len(m) == 0 {
		return
	}
	fmt.Printf("\n%s:\n", title)
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "  KEY\tREQUESTS\tCOST")
	for _, k := range stats.SortedKeys(m) {
		g := m[k]
		fmt.Fprintf(w, "  %s\t%d\t$%.4f\n", k, g.Requests, g.Cost)
	}
	w.Flush()
}
