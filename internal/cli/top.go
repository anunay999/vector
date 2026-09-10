package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

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
		Long: "A refreshing dashboard over local telemetry.\n" +
			"Keys: q quit, p pause, r refresh, f filter by role, v filter by provider, a clear filters.\n" +
			"Use --once to print a single frame (for screenshots or scripts).",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if once {
				snap, serr := stats.Collect(cfg, since, 14, stats.Filter{})
				w, _ := termSize()
				fmt.Print(frame(cfg, snap, serr, w, 0, probeHealth(cfg), false, false, stats.Filter{}))
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
	var filter stats.Filter
	roleOptions := append([]string{""}, cfg.RoleNames()...)
	provOptions := append([]string{""}, cfg.ProviderIDs()...)
	cycle := func(options []string, current string) string {
		for i, o := range options {
			if o == current {
				return options[(i+1)%len(options)]
			}
		}
		return options[0]
	}

	render := func() {
		w, h := termSize()
		snap, err := stats.Collect(cfg, since, 14, filter)
		fmt.Fprint(os.Stdout, frame(cfg, snap, err, w, h, probeHealth(cfg), paused, true, filter))
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
			case 'f':
				filter.Role = cycle(roleOptions, filter.Role)
				render()
			case 'v':
				filter.Provider = cycle(provOptions, filter.Provider)
				render()
			case 'a':
				filter = stats.Filter{}
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

func frame(cfg *config.Config, snap stats.Snapshot, readErr error, width, height int, gatewayUp, paused, live bool, f stats.Filter) string {
	if width < 40 {
		width = 40
	}
	var b strings.Builder

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
	filterDesc := ""
	if f.Role != "" || f.Provider != "" || f.Model != "" {
		var parts []string
		if f.Role != "" {
			parts = append(parts, "role="+f.Role)
		}
		if f.Provider != "" {
			parts = append(parts, "provider="+f.Provider)
		}
		if f.Model != "" {
			parts = append(parts, "model="+f.Model)
		}
		filterDesc = "   " + cYellow + "filter " + strings.Join(parts, " ") + cReset
	}
	fmt.Fprintf(&b, "%svector top%s   routing %s%s%s   gateway %s%s%s   window %s%s%s%s\n",
		cBold, cReset, rc, routing, cReset, gc, gw, cReset, cDim, humanDuration(snap.Since), cReset+pause, filterDesc)
	b.WriteString(cDim + strings.Repeat("─", width) + cReset + "\n")

	if readErr != nil {
		fmt.Fprintf(&b, "%serror reading telemetry: %v%s\n", cRed, readErr, cReset)
	}
	// Summary with an off-plan gauge.
	fmt.Fprintf(&b, " %s$%.4f%s  off-plan %s %s%.0f%%%s  req %d  tokens %s  tok/s %s%.0f%s  err %s  fb %s\n",
		cBold, snap.Cost, cReset,
		barChart(snap.OffPlanPct()/100, 16, cGreen), cCyan, snap.OffPlanPct(), cReset,
		snap.Requests, humanInt(snap.Tokens()), cCyan, snap.TokensPerSec(), cReset,
		colorCount(snap.Errors, cRed), colorCount(snap.Fallbacks, cYellow))
	fmt.Fprintf(&b, " %s%.1f req/min%s   refreshed %s\n", cDim, snap.PerMinute(), cReset, snap.Generated.Format("15:04:05"))
	b.WriteString(cDim + strings.Repeat("─", width) + cReset + "\n")

	// Throughput sparklines.
	reqVals := make([]float64, 0, len(snap.Buckets))
	costVals := make([]float64, 0, len(snap.Buckets))
	for _, bk := range snap.Buckets {
		reqVals = append(reqVals, float64(bk.Requests))
		costVals = append(costVals, bk.Cost)
	}
	fmt.Fprintf(&b, "%s THROUGHPUT%s%s last 30m%s\n", cBold, cReset, cDim, cReset)
	fmt.Fprintf(&b, "  req   %s  %s%.1f/min%s\n", sparkline(reqVals, cCyan), cDim, snap.PerMinute(), cReset)
	fmt.Fprintf(&b, "  cost  %s  %s%s%s\n", sparkline(costVals, cGreen), cDim, money(snap.Cost), cReset)
	b.WriteString(cDim + strings.Repeat("─", width) + cReset + "\n")

	// Model mix bars + provider summary.
	b.WriteString(cBold + " MODEL MIX" + cReset + "\n")
	models := stats.SortedKeys(snap.ByModel)
	if len(models) == 0 {
		fmt.Fprintf(&b, "  %s(no requests yet)%s\n", cDim, cReset)
	}
	maxReq := 1
	for _, m := range models {
		if g := snap.ByModel[m]; g.Requests > maxReq {
			maxReq = g.Requests
		}
	}
	for i, m := range models {
		if i >= 5 {
			break
		}
		g := snap.ByModel[m]
		share := 0.0
		if snap.Requests > 0 {
			share = 100 * float64(g.Requests) / float64(snap.Requests)
		}
		fmt.Fprintf(&b, "  %-30s %s %5d %4.0f%%\n",
			truncate(m, 30), barChart(float64(g.Requests)/float64(maxReq), 20, cCyan), g.Requests, share)
	}
	if provKeys := stats.SortedKeys(snap.ByProvider); len(provKeys) > 0 {
		var parts []string
		for _, p := range provKeys {
			label := p
			if isNative(cfg, p) {
				label += "*"
			}
			share := 0.0
			if snap.Requests > 0 {
				share = 100 * float64(snap.ByProvider[p].Requests) / float64(snap.Requests)
			}
			parts = append(parts, fmt.Sprintf("%s %d (%.0f%%)", label, snap.ByProvider[p].Requests, share))
		}
		fmt.Fprintf(&b, "  %sproviders: %s%s\n", cDim, strings.Join(parts, "  ·  "), cReset)
	}
	b.WriteString(cDim + strings.Repeat("─", width) + cReset + "\n")

	// Cost per day histogram.
	b.WriteString(cBold + " COST / DAY" + cReset + cDim + " last 14d" + cReset)
	if len(snap.Daily) > 0 {
		costs := make([]float64, len(snap.Daily))
		total, peak := 0.0, 0.0
		for i, d := range snap.Daily {
			costs[i] = d.Cost
			total += d.Cost
			if d.Cost > peak {
				peak = d.Cost
			}
		}
		fmt.Fprintf(&b, "   %stotal %s   peak %s%s\n", cDim, money(total), money(peak), cReset)
		for _, row := range verticalBars(costs, 5) {
			fmt.Fprintf(&b, "   %s%s%s\n", cGreen, row, cReset)
		}
		first := snap.Daily[0].Date.Format("01-02")
		last := snap.Daily[len(snap.Daily)-1].Date.Format("01-02")
		pad := len(costs) - len(first) - len(last)
		if pad < 1 {
			pad = 1
		}
		fmt.Fprintf(&b, "   %s%s%s%s%s\n", cDim, first, strings.Repeat(" ", pad), last, cReset)
	} else {
		fmt.Fprintf(&b, "   %s(no data)%s\n", cDim, cReset)
	}
	b.WriteString(cDim + strings.Repeat("─", width) + cReset + "\n")

	// Roles.
	b.WriteString(cBold + " ROLES" + cReset + "\n")
	fmt.Fprintf(&b, "  %-16s %-32s %5s %8s %6s %9s %11s %3s\n", "role", "model", "req", "tokens", "tok/s", "cost", "p50/p95", "err")
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
		fmt.Fprintf(&b, "  %-16s %-32s %5d %8s %6.0f %9s %11s %3s\n",
			truncate(role, 16), truncate(model, 32), g.Requests,
			humanInt(g.InputTokens+g.OutputTokens), g.TokensPerSec(), money(g.Cost),
			fmt.Sprintf("%s/%s", ms(g.P50()), ms(g.P95())),
			colorCount(g.Errors, cRed))
	}
	b.WriteString(cDim + strings.Repeat("─", width) + cReset + "\n")

	// Recent. Cap the row count so a tall frame never exceeds the terminal height:
	// a scroll on the alternate screen desyncs the cursor-home repaint.
	recent := snap.Recent
	if height > 0 {
		room := height - strings.Count(b.String(), "\n") - 3 // title + separator + keys
		if room < 0 {
			room = 0
		}
		if len(recent) > room {
			recent = recent[:room]
		}
	}
	b.WriteString(cBold + " RECENT" + cReset + "\n")
	if len(snap.Recent) == 0 {
		fmt.Fprintf(&b, "  %s(no requests yet)%s\n", cDim, cReset)
	}
	for _, r := range recent {
		model := r.RoutedModel
		if r.RequestedModel != "" && r.RequestedModel != r.RoutedModel {
			model = r.RequestedModel + " -> " + r.RoutedModel
		}
		status := fmt.Sprintf("%d", r.Status)
		if r.Error != "" {
			status = fmt.Sprintf("%d %s", r.Status, truncate(r.Error, 24))
		}
		fmt.Fprintf(&b, "  %s  %-12s %-38s %-12s %6s %9s  %s\n",
			r.Time.Format("15:04:05"), truncate(r.Role, 12), truncate(model, 38),
			truncate(r.Provider, 12), fmt.Sprintf("%d/%d", r.InputTokens, r.OutputTokens),
			money(r.EstCostUSD), status)
	}

	b.WriteString(cDim + strings.Repeat("─", width) + cReset + "\n")
	fmt.Fprintf(&b, " %sq quit   p pause   r refresh   f role   v provider   a clear%s\n", cDim, cReset)

	// Fit each line to the terminal width and, for the live view, end it with CRLF.
	// Raw mode clears OPOST, so a lone \n moves down without returning to column 0
	// and the whole frame staircases; \x1b[K erases any stale glyphs to end-of-line.
	lines := strings.Split(b.String(), "\n")
	for i := range lines {
		lines[i] = fitLine(lines[i], width)
	}
	if live {
		return "\x1b[H" + strings.Join(lines, "\x1b[K\r\n") + "\x1b[K\x1b[J"
	}
	return strings.Join(lines, "\n")
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

// fitLine limits a rendered line to at most width display columns and always
// resets styling. It is ANSI-aware: escape sequences cost no columns, and
// truncation never lands mid-escape or mid-rune. Callers guarantee no line
// exceeds the terminal width, so the terminal never auto-wraps.
func fitLine(s string, width int) string {
	if width <= 0 {
		return s
	}
	styled := strings.IndexByte(s, 0x1b) >= 0

	total := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i = skipEscape(s, i)
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		total += runeWidth(r)
		i += size
	}
	if total <= width {
		if styled {
			return s + cReset
		}
		return s
	}
	if width == 1 {
		return "…"
	}

	var b strings.Builder
	vis := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := skipEscape(s, i)
			b.WriteString(s[i:j])
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		w := runeWidth(r)
		if vis+w > width-1 {
			break
		}
		b.WriteRune(r)
		vis += w
		i += size
	}
	b.WriteString("…")
	if styled {
		b.WriteString(cReset)
	}
	return b.String()
}

// skipEscape returns the index just past the ANSI escape sequence starting at i
// (which must be an ESC byte), handling both CSI (ESC [ ... final) and two-byte
// escapes.
func skipEscape(s string, i int) int {
	j := i + 1
	if j < len(s) && s[j] == '[' {
		j++
		for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
			j++
		}
		if j < len(s) {
			j++
		}
		return j
	}
	if j < len(s) {
		return j + 1
	}
	return j
}

// runeWidth returns the display width of r: 0 for control/combining, 2 for East
// Asian wide, else 1. The dashboard's box-drawing and block glyphs are all 1.
func runeWidth(r rune) int {
	switch {
	case r < 0x20 || (r >= 0x7f && r < 0xa0):
		return 0
	case r >= 0x0300 && r <= 0x036f:
		return 0
	case r == 0x2329 || r == 0x232a:
		return 2
	case r >= 0x1100 && r <= 0x115f:
		return 2
	case r >= 0x2e80 && r <= 0xa4cf && r != 0x303f:
		return 2
	case r >= 0xac00 && r <= 0xd7a3:
		return 2
	case r >= 0xf900 && r <= 0xfaff:
		return 2
	case r >= 0xfe30 && r <= 0xfe6f:
		return 2
	case r >= 0xff00 && r <= 0xff60:
		return 2
	case r >= 0xffe0 && r <= 0xffe6:
		return 2
	}
	return 1
}

func colorCount(n int, color string) string {
	if n == 0 {
		return "0"
	}
	return fmt.Sprintf("%s%d%s", color, n, cReset)
}

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// sparkline renders values as a compact block chart; the tallest value maps to
// a full block.
func sparkline(vals []float64, color string) string {
	if len(vals) == 0 {
		return ""
	}
	min, max := vals[0], vals[0]
	for _, v := range vals {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	var b strings.Builder
	b.WriteString(color)
	for _, v := range vals {
		idx := 0
		if max > min {
			idx = int((v-min)/(max-min)*float64(len(sparkRunes)-1) + 0.5)
		}
		b.WriteRune(sparkRunes[idx])
	}
	b.WriteString(cReset)
	return b.String()
}

// barChart renders a 0..1 fraction as a filled/empty block bar.
func barChart(frac float64, width int, color string) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	return color + strings.Repeat("█", filled) + cDim + strings.Repeat("░", width-filled) + cReset
}

// verticalBars renders one column per value as a stack of blocks, tallest value
// = full height. Rows are returned top-first.
func verticalBars(vals []float64, height int) []string {
	peak := 0.0
	for _, v := range vals {
		if v > peak {
			peak = v
		}
	}
	if peak <= 0 {
		peak = 1
	}
	rows := make([]string, height)
	for r := 0; r < height; r++ {
		var sb strings.Builder
		for _, v := range vals {
			filled := int(v/peak*float64(height) + 0.5)
			if filled >= height-r {
				sb.WriteString("█")
			} else {
				sb.WriteString(" ")
			}
		}
		rows[r] = sb.String()
	}
	return rows
}
