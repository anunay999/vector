package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/anunay999/vector/internal/telemetry"
	"github.com/spf13/cobra"
)

func newTelemetryCmd() *cobra.Command {
	var (
		since    time.Duration
		asJSON   bool
		follow   bool
		limit    int
		role     string
		provider string
		showPath bool
	)
	cmd := &cobra.Command{
		Use:   "telemetry",
		Short: "Show raw request telemetry",
		Long: "Reads the local JSONL request store (~/.config/vector/telemetry/).\n" +
			"Use --json for machine output, --follow to stream, and --since to widen\n" +
			"the window. 'vector spend' is the aggregated view of the same data.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			dir := cfg.TelemetryDir()
			if showPath {
				fmt.Println(dir)
				return nil
			}
			rec := telemetry.New(dir, cfg.Telemetry.Enabled)

			if follow {
				ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
				defer stop()
				pathFor := func() string {
					return filepath.Join(dir, "requests-"+time.Now().Format("2006-01-02")+".jsonl")
				}
				fmt.Fprintf(os.Stderr, "following %s (ctrl-c to stop)\n", dir)
				return followFile(ctx, pathFor, os.Stdout)
			}

			records, err := rec.ReadSince(time.Now().Add(-since))
			if err != nil {
				return err
			}
			records = filterRecords(records, role, provider)
			sort.Slice(records, func(i, j int) bool { return records[i].Time.After(records[j].Time) })
			if limit > 0 && len(records) > limit {
				records = records[:limit]
			}

			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(records)
			}
			printRecords(records, since)
			return nil
		},
	}
	cmd.Flags().DurationVar(&since, "since", 24*time.Hour, "window (e.g. 1h, 24h, 168h)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "stream new records as JSONL")
	cmd.Flags().IntVarP(&limit, "limit", "n", 50, "max records to show (0 = all)")
	cmd.Flags().StringVar(&role, "role", "", "filter by role")
	cmd.Flags().StringVar(&provider, "provider", "", "filter by provider")
	cmd.Flags().BoolVar(&showPath, "path", false, "print the telemetry directory")
	return cmd
}

func filterRecords(in []telemetry.Record, role, provider string) []telemetry.Record {
	if role == "" && provider == "" {
		return in
	}
	out := make([]telemetry.Record, 0, len(in))
	for _, r := range in {
		if role != "" && r.Role != role {
			continue
		}
		if provider != "" && r.Provider != provider {
			continue
		}
		out = append(out, r)
	}
	return out
}

func printRecords(records []telemetry.Record, since time.Duration) {
	if len(records) == 0 {
		fmt.Printf("no telemetry in the last %s\n", since)
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tHARNESS\tROLE\tMODEL\tPROVIDER\tTOKENS\tCOST\tMS\tSTATUS")
	for _, r := range records {
		model := r.RoutedModel
		if r.RequestedModel != "" && r.RequestedModel != r.RoutedModel {
			model = r.RequestedModel + " -> " + r.RoutedModel
		}
		status := fmt.Sprintf("%d", r.Status)
		if r.Error != "" {
			status = fmt.Sprintf("%d %s", r.Status, truncateStr(r.Error, 40))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d/%d\t$%.4f\t%d\t%s\n",
			r.Time.Format("15:04:05"), r.Harness, r.Role, model, r.Provider,
			r.InputTokens, r.OutputTokens, r.EstCostUSD, r.LatencyMS, status)
	}
	w.Flush()
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
