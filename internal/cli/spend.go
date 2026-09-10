package cli

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/anunay999/vector/internal/telemetry"
	"github.com/spf13/cobra"
)

type spendAgg struct {
	requests int
	tokens   int
	cost     float64
	subagent int
}

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
			rec := telemetry.New(cfg.TelemetryDir(), cfg.Telemetry.Enabled)
			records, err := rec.ReadSince(time.Now().Add(-since))
			if err != nil {
				return err
			}

			byProvider := map[string]*spendAgg{}
			byRole := map[string]*spendAgg{}
			byModel := map[string]*spendAgg{}
			var total spendAgg
			for _, r := range records {
				total.requests++
				total.tokens += r.InputTokens + r.OutputTokens
				total.cost += r.EstCostUSD
				if r.Role != "" && r.Role != "architect" && r.Role != "lead" {
					total.subagent++
				}
				bump(byProvider, r.Provider, r)
				bump(byRole, r.Role, r)
				bump(byModel, r.RoutedModel, r)
			}

			if asJSON {
				fmt.Printf("{\"since\":%q,\"requests\":%d,\"tokens\":%d,\"estimated_cost_usd\":%.4f}\n",
					since.String(), total.requests, total.tokens, total.cost)
				return nil
			}

			fmt.Printf("Since %s: %d requests, %d tokens, est. $%.4f\n",
				time.Now().Add(-since).Format("2006-01-02 15:04"), total.requests, total.tokens, total.cost)
			if total.requests > 0 {
				fmt.Printf("Off-plan (subagent) share: %.0f%%\n", 100*float64(total.subagent)/float64(total.requests))
			}
			printAgg("By provider", byProvider)
			printAgg("By role", byRole)
			printAgg("By model", byModel)
			return nil
		},
	}
	cmd.Flags().DurationVar(&since, "since", 24*time.Hour, "window (e.g. 24h, 168h)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}

func bump(m map[string]*spendAgg, key string, r telemetry.Record) {
	if key == "" {
		key = "(none)"
	}
	if m[key] == nil {
		m[key] = &spendAgg{}
	}
	m[key].requests++
	m[key].cost += r.EstCostUSD
}

func printAgg(title string, m map[string]*spendAgg) {
	if len(m) == 0 {
		return
	}
	fmt.Printf("\n%s:\n", title)
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "  KEY\tREQUESTS\tCOST")
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := m[k]
		fmt.Fprintf(w, "  %s\t%d\t$%.4f\n", k, v.requests, v.cost)
	}
	w.Flush()
}
