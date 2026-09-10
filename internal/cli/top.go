package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/stats"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// ANSI styling for the dashboard chrome (table rows stay plain so truncation is
// byte-safe).
const (
	cReset  = "\x1b[0m"
	cBold   = "\x1b[1m"
	cDim    = "\x1b[2m"
	cGreen  = "\x1b[32m"
	cRed    = "\x1b[31m"
	cYellow = "\x1b[33m"
	cCyan   = "\x1b[36m"
)

func newTopCmd() *cobra.Command {
	var since, interval time.Duration
	var once bool
	cmd := &cobra.Command{
		Use:   "top",
		Short: "Live dashboard of routing, spend, and recent requests",
		Long: "A refreshing dashboard over local telemetry. Keys: q quit, p pause,\n" +
			"r refresh. Use --once to print a single frame (for screenshots or scripts).",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if once {
				snap, serr := stats.Collect(cfg, since, 14)
				w, _ := termSize()
				fmt.Print(frame(cfg, snap, serr, w, probeHealth(cfg), false, false))
				return nil
			}
			return runTop(cfg, since, interval)
		},
	}
	cmd.Flags().DurationVar(&since, "since", 24*time.Hour, "aggregation window")
	cmd.Flags().DurationVar(&interval, "interval", time.Second, "refresh interval")
	cmd.Flags().BoolVar(&once, "once", false, "print one frame and exit")
	return cmd
}

func runTop(cfg *config.Config, since, interval time.Duration) error {
	fd := int(os.Stdin.Fd())
	tty := term.IsTerminal(fd)
	var restore func()
	if tty {
		if old, err := term.MakeRaw(fd); err == nil {
			restore = func() { _ = term.Restore(fd, old) }
		}
		fmt.Fprint(os.Stdout, "\x1b[?1049h\x1b[?25l") // alt screen, hide cursor
	}
	defer func() {
		if tty {
			fmt.Fprint(os.Stdout, "\x1b[?25h\x1b[?1049l")
		}
		if restore != nil {
			restore()
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	keys := make(chan byte, 16)
	if tty {
		go func() {
			b := make([]byte, 1)
			for {
				n, err := os.Stdin.Read(b)
				if err != nil {
					return
				}
				if n == 1 {
					keys <- b[0]
				}
			}
		}()
	}

	paused := false
	render := func() {
		w, _ := termSize()
		snap, err := stats.Collect(cfg, since, 14)
		fmt.Fprint(os.Stdout, frame(cfg, snap, err, w, probeHealth(cfg), paused, true))
	}
	render()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case b := <-keys:
			switch b {
			case 'q', 3: // q or ctrl-c (raw mode delivers ctrl-c as byte 3)
				return nil
			case 'p':
				paused = !paused
				render()
			case 'r':
				render()
			}
		case <-ticker.C:
			if !paused {
				render()
			}
		}
	}
}

func termSize() (int, int) {
	fd := int(os.Stdout.Fd())
	if w, h, err := term.GetSize(fd); err == nil && w > 0 {
		return w, h
	}
	return 120, 40
}

func frame(cfg *config.Config, snap stats.Snapshot, readErr error, width int, gatewayUp, paused, live bool) string {
	if width < 80 {
		width = 80
	}
	var b strings.Builder
	if live {
		b.WriteString("\x1b[H") // cursor home; we repaint every line
	}

	// Header.
	routing, rc := "ON", cGreen
	if !cfg.RoutingEnabled {
		routing, rc = "OFF", cRed
	}
	gw, gc := "up", cGreen
	if !gatewayUp {
		gw, gc = "down", cRed
	}
	pause := ""
	if paused {
		pause = "   " + cYellow + "PAUSED" + cReset
	}
	fmt.Fprintf(&b, "%svector top%s   routing %s%s%s   gateway %s%s%s   window %s%s%s\n",
		cBold, cReset, rc, routing, cReset, gc, gw, cReset, cDim, humanDuration(snap.Since), cReset+pause)
	b.WriteString(cDim + strings.Repeat("─", width) + cReset + "\n")

	if readErr != nil {
		fmt.Fprintf(&b, "%serror reading telemetry: %v%s\n", cRed, readErr, cReset)
	}
	// Summary.
	fmt.Fprintf(&b, " %s$%.4f%s   off-plan %s%.0f%%%s   requests %d   tokens %s   errors %s   fallbacks %s\n",
		cBold, snap.Cost, cReset, cCyan, snap.OffPlanPct(), cReset,
		snap.Requests, humanInt(snap.Tokens()), colorCount(snap.Errors, cRed), colorCount(snap.Fallbacks, cYellow))
	fmt.Fprintf(&b, " %s%.1f req/min%s   refreshed %s\n",
		cDim, snap.PerMinute(), cReset, snap.Generated.Format("15:04:05"))
	b.WriteString(cDim + strings.Repeat("─", width) + cReset + "\n")

	// Roles.
	b.WriteString(cBold + " ROLES" + cReset + "\n")
	fmt.Fprintf(&b, "  %-16s %-34s %6s %8s %9s %11s %4s\n", "role", "model", "req", "tokens", "cost", "p50/p95", "err")
	roleKeys := stats.SortedKeys(snap.ByRole)
	if len(roleKeys) == 0 {
		fmt.Fprintf(&b, "  %s(no requests yet)%s\n", cDim, cReset)
	}
	for _, role := range roleKeys {
		g := snap.ByRole[role]
		model := snap.RoleModel[role]
		if model == "" {
			model = "-"
		}
		fmt.Fprintf(&b, "  %-16s %-34s %6d %8s %9s %11s %4s\n",
			truncate(role, 16), truncate(model, 34), g.Requests,
			humanInt(g.InputTokens+g.OutputTokens), money(g.Cost),
			fmt.Sprintf("%s/%s", ms(g.P50()), ms(g.P95())),
			colorCount(g.Errors, cRed))
	}
	b.WriteString(cDim + strings.Repeat("─", width) + cReset + "\n")

	// Providers.
	b.WriteString(cBold + " PROVIDERS" + cReset + "\n")
	provKeys := stats.SortedKeys(snap.ByProvider)
	if len(provKeys) == 0 {
		fmt.Fprintf(&b, "  %s(none)%s\n", cDim, cReset)
	}
	for _, p := range provKeys {
		g := snap.ByProvider[p]
		label := p
		if isNative(cfg, p) {
			label += " (native)"
		}
		fmt.Fprintf(&b, "  %-24s %6d req %8s %9s %4s\n", truncate(label, 24), g.Requests, humanInt(g.InputTokens+g.OutputTokens), money(g.Cost), colorCount(g.Errors, cRed))
	}
	b.WriteString(cDim + strings.Repeat("─", width) + cReset + "\n")

	// Recent.
	b.WriteString(cBold + " RECENT" + cReset + "\n")
	if len(snap.Recent) == 0 {
		fmt.Fprintf(&b, "  %s(no requests yet)%s\n", cDim, cReset)
	}
	for _, r := range snap.Recent {
		model := r.RoutedModel
		if r.RequestedModel != "" && r.RequestedModel != r.RoutedModel {
			model = r.RequestedModel + " -> " + r.RoutedModel
		}
		status := fmt.Sprintf("%d", r.Status)
		if r.Error != "" {
			status = fmt.Sprintf("%d %s", r.Status, truncate(r.Error, 24))
		}
		line := fmt.Sprintf("  %s  %-12s %-38s %-12s %6s %9s  %s",
			r.Time.Format("15:04:05"), truncate(r.Role, 12), truncate(model, 38),
			truncate(r.Provider, 12), fmt.Sprintf("%d/%d", r.InputTokens, r.OutputTokens),
			money(r.EstCostUSD), status)
		fmt.Fprintln(&b, truncateANSI(line, width))
	}

	b.WriteString(cDim + strings.Repeat("─", width) + cReset + "\n")
	fmt.Fprintf(&b, " %sq quit   p pause   r refresh%s\n", cDim, cReset)
	if live {
		b.WriteString("\x1b[J") // clear anything below
	}
	return b.String()
}

func isNative(cfg *config.Config, providerID string) bool {
	for _, p := range cfg.Providers {
		if p.ID == providerID {
			return p.Native
		}
	}
	return false
}

func humanDuration(since time.Time) string {
	d := time.Since(since)
	if d <= 0 {
		return "all"
	}
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
}

func humanInt(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func money(v float64) string {
	if v == 0 {
		return "-"
	}
	if v < 0.01 {
		return fmt.Sprintf("$%.5f", v)
	}
	return fmt.Sprintf("$%.4f", v)
}

func ms(v int64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.1fs", float64(v)/1000)
	}
	return fmt.Sprintf("%dms", v)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// truncateANSI truncates a plain (unstyled) line to width, keeping it safe when
// the terminal is narrow.
func truncateANSI(s string, width int) string {
	if len(s) <= width {
		return s
	}
	if width <= 1 {
		return s[:width]
	}
	return s[:width-1] + "…"
}

func colorCount(n int, color string) string {
	if n == 0 {
		return "0"
	}
	return fmt.Sprintf("%s%d%s", color, n, cReset)
}
