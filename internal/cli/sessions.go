package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/anunay999/vector/internal/telemetry"
	"github.com/spf13/cobra"
)

// newSessionsCmd lists the client sessions seen in telemetry, so it is obvious
// which Claude/Codex session a given request came from.
func newSessionsCmd() *cobra.Command {
	var since time.Duration
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "List client sessions seen in telemetry (id, checkout, activity)",
		Long: "Groups recent telemetry by the client session that produced it.\n" +
			"Session ids come from Claude Code's X-Claude-Code-Session-Id header\n" +
			"and Codex's session-id header; the checkout is read from the request.",
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
			rows := aggregateSessions(records)
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			if len(rows) == 0 {
				fmt.Printf("no sessions in the last %s\n", since)
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "SESSION\tCHECKOUT\tHARNESS\tREQ\tROLES\tPROVIDERS\tCOST\tLAST")
			for _, r := range rows {
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t$%.4f\t%s\n",
					shortSession(r.Session), r.Checkout, r.Harness, r.Requests,
					r.Roles, r.Providers, r.Cost, r.Last.Format("15:04:05"))
			}
			w.Flush()
			return nil
		},
	}
	cmd.Flags().DurationVar(&since, "since", 6*time.Hour, "window (e.g. 1h, 24h)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}

type sessionRow struct {
	Session   string    `json:"session"`
	Checkout  string    `json:"checkout"`
	Harness   string    `json:"harness"`
	Requests  int       `json:"requests"`
	Roles     string    `json:"roles"`
	Providers string    `json:"providers"`
	Cost      float64   `json:"cost"`
	Last      time.Time `json:"last"`
}

func aggregateSessions(records []telemetry.Record) []sessionRow {
	type agg struct {
		row   sessionRow
		roles map[string]int
		provs map[string]int
	}
	m := map[string]*agg{}
	for _, r := range records {
		key := r.Session
		if key == "" {
			key = "unknown"
		}
		a := m[key]
		if a == nil {
			a = &agg{
				row:   sessionRow{Session: r.Session, Checkout: baseName(r.Project), Harness: r.Harness},
				roles: map[string]int{},
				provs: map[string]int{},
			}
			m[key] = a
		}
		if a.row.Checkout == "-" {
			a.row.Checkout = baseName(r.Project)
		}
		a.row.Requests++
		a.row.Cost += r.EstCostUSD
		if r.Role != "" {
			a.roles[r.Role]++
		}
		if r.Provider != "" {
			a.provs[r.Provider]++
		}
		if r.Time.After(a.row.Last) {
			a.row.Last = r.Time
		}
	}
	out := make([]sessionRow, 0, len(m))
	for _, a := range m {
		a.row.Roles = topKeys(a.roles, 3)
		a.row.Providers = topKeys(a.provs, 2)
		out = append(out, a.row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}

// topKeys renders the most frequent keys as "key count" pairs, most frequent
// first, up to n.
func topKeys(m map[string]int, n int) string {
	type kv struct {
		k string
		v int
	}
	items := make([]kv, 0, len(m))
	for k, v := range m {
		items = append(items, kv{k, v})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v != items[j].v {
			return items[i].v > items[j].v
		}
		return items[i].k < items[j].k
	})
	if len(items) > n {
		items = items[:n]
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, fmt.Sprintf("%s %d", it.k, it.v))
	}
	return strings.Join(parts, ", ")
}
