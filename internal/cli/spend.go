package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
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
				out := map[string]any{
					"since": since.String(), "requests": snap.Requests, "tokens": snap.Tokens(),
					"estimated_cost_usd": snap.Cost, "off_plan_pct": snap.OffPlanPct(),
					"off_plan_tokens": snap.OffPlanInputTokens + snap.OffPlanOutputTokens,
					"cache_hit_pct":   snap.CacheHitPct(), "tool_search_pct": snap.ToolSearchPct(),
					"savings_usd": snap.SavingsUSD, "savings_priced_requests": snap.SavingsPriced,
					"savings_unpriced_requests": snap.SavingsUnpriced, "guard_events": snap.GuardEvents,
				}
				enc := json.NewEncoder(os.Stdout)
				return enc.Encode(out)
			}

			fmt.Printf("Since %s: %d requests, %d tokens, est. $%.4f\n",
				snap.Since.Format("2006-01-02 15:04"), snap.Requests, snap.Tokens(), snap.Cost)
			if snap.Requests > 0 {
				fmt.Printf("Off-plan (non-subscription) share: %.0f%%  (%d tokens kept off the subscription)\n",
					snap.OffPlanPct(), snap.OffPlanInputTokens+snap.OffPlanOutputTokens)
				fmt.Printf("Prompt cache hit: %.0f%%   tool-search requests: %.0f%%\n", snap.CacheHitPct(), snap.ToolSearchPct())
				switch {
				case snap.SavingsPriced > 0 && snap.SavingsUnpriced == 0:
					fmt.Printf("Estimated saving vs reference price: $%.4f\n", snap.SavingsUSD)
				case snap.SavingsPriced > 0:
					fmt.Printf("Estimated saving vs reference price: ≥ $%.4f (%d off-plan requests unpriced)\n", snap.SavingsUSD, snap.SavingsUnpriced)
				case snap.OffPlan > 0:
					fmt.Println("Estimated saving: n/a — no list price known for the requested models; add reference_prices (see vector models reference)")
				}
				for _, k := range sortedKeys(snap.GuardEvents) {
					fmt.Printf("Guard events: %s ×%d\n", k, snap.GuardEvents[k])
				}
			}
			printGroups("By provider", snap.ByProvider)
			printGroups("By role", snap.ByRole)
			printGroups("By model", snap.ByModel)
			printDaily(snap.Daily)
			return nil
		},
	}
	cmd.Flags().DurationVar(&since, "since", 24*time.Hour, "window (e.g. 24h, 168h)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func printDaily(days []stats.Day) {
	any := false
	for _, d := range days {
		if d.Requests > 0 {
			any = true
			break
		}
	}
	if !any {
		return
	}
	fmt.Printf("\nBy day (last %dd):\n", len(days))
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "  DATE\tREQUESTS\tTOKENS\tCOST")
	var sum float64
	for _, d := range days {
		if d.Requests == 0 {
			continue
		}
		fmt.Fprintf(w, "  %s\t%d\t%d\t$%.4f\n", d.Date.Format("2006-01-02"), d.Requests, d.Tokens, d.Cost)
		sum += d.Cost
	}
	fmt.Fprintf(w, "  %s\t\t\t$%.4f\n", "total", sum)
	w.Flush()
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
