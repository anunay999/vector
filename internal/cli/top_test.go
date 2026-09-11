package cli

import (
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/stats"
	"github.com/anunay999/vector/internal/telemetry"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func TestBarChartFill(t *testing.T) {
	b := stripANSI(barChart(0.5, 10, ""))
	if strings.Count(b, "█") != 5 || strings.Count(b, "░") != 5 {
		t.Fatalf("bar(0.5,10) = %q, want 5 filled + 5 empty", b)
	}
	if got := stripANSI(barChart(2, 10, "")); strings.Count(got, "█") != 10 {
		t.Fatalf("clamp high failed: %q", got)
	}
	if got := stripANSI(barChart(-1, 10, "")); strings.Count(got, "░") != 10 {
		t.Fatalf("clamp low failed: %q", got)
	}
}

func TestSparklineLengthAndPeak(t *testing.T) {
	out := stripANSI(sparkline([]float64{0, 1, 2, 3, 4}, ""))
	if n := utf8.RuneCountInString(out); n != 5 {
		t.Fatalf("sparkline runes = %d, want 5 (%q)", n, out)
	}
	if !strings.Contains(out, "█") {
		t.Fatalf("expected a full block for the peak: %q", out)
	}
	if !strings.HasPrefix(out, "▁") {
		t.Fatalf("expected the minimum to be the lowest block: %q", out)
	}
}

func TestVerticalBars(t *testing.T) {
	rows := verticalBars([]float64{0, 2, 1}, 4)
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(rows))
	}
	top := []rune(rows[0])
	bottom := []rune(rows[3])
	// Peak column (index 1) is full height; zero column (index 0) is empty.
	if top[0] != ' ' || top[1] != '█' || top[2] != ' ' {
		t.Fatalf("top row = %q", rows[0])
	}
	if bottom[0] != ' ' || bottom[1] != '█' || bottom[2] != '█' {
		t.Fatalf("bottom row = %q", rows[3])
	}
}

func visibleCols(s string) int {
	return utf8.RuneCountInString(strings.ReplaceAll(stripANSI(s), "\r", ""))
}

func testSnapshot() stats.Snapshot {
	now := time.Now()
	rec := func(min int, role, model string, in, out int, cost float64) telemetry.Record {
		return telemetry.Record{
			Time: now.Add(-time.Duration(min) * time.Minute), Role: role,
			RequestedModel: "vector-" + role, RoutedModel: model, Provider: "openrouter",
			Status: 200, InputTokens: in, OutputTokens: out, EstCostUSD: cost, LatencyMS: 500,
		}
	}
	return stats.Snapshot{
		Since: now.Add(-time.Hour), Generated: now,
		Requests: 3, InputTokens: 100, OutputTokens: 50, Cost: 0.001,
		OffPlan: 2, LastMinute: 1, TotalLatencyMS: 1500,
		ByModel: map[string]*stats.Group{
			"deepseek/deepseek-v4.1-flash": {Requests: 2, InputTokens: 60, OutputTokens: 40, Cost: 0.0008, Latencies: []int64{500, 600}},
			"z-ai/glm-5.3-flash":           {Requests: 1, InputTokens: 40, OutputTokens: 10, Cost: 0.0002, Latencies: []int64{200}},
		},
		ByProvider: map[string]*stats.Group{"openrouter": {Requests: 3}},
		ByRole: map[string]*stats.Group{
			"worker": {Requests: 2, InputTokens: 60, OutputTokens: 40, Cost: 0.0008, Latencies: []int64{500, 600}},
			"scout":  {Requests: 1, InputTokens: 40, OutputTokens: 10, Cost: 0.0002, Latencies: []int64{200}},
		},
		RoleModel: map[string]string{"worker": "deepseek/deepseek-v4.1-flash", "scout": "z-ai/glm-5.3-flash"},
		Buckets:   make([]stats.Bucket, stats.BucketCount),
		Daily:     make([]stats.Day, stats.HistogramDays),
		Recent: []telemetry.Record{
			rec(1, "worker", "deepseek/deepseek-v4.1-flash", 31, 8, 0.00001),
			rec(2, "scout", "z-ai/glm-5.3-flash", 13, 8, 0.00001),
			rec(3, "worker", "z-ai/glm-5.3-flash", 1, 1, 0),
		},
	}
}

// Every rendered row must fit the terminal width or the terminal wraps it and
// the frame desyncs — the bug that made the live dashboard unreadable.
func TestFrameNeverExceedsWidth(t *testing.T) {
	cfg := &config.Config{}
	snap := testSnapshot()
	for _, w := range []int{40, 60, 80, 100, 120} {
		for _, live := range []bool{false, true} {
			out := frame(cfg, snap, nil, w, 0, true, false, live, stats.Filter{})
			for _, ln := range strings.Split(out, "\n") {
				if got := visibleCols(ln); got > w {
					t.Fatalf("width=%d live=%v: %d cols > width: %q", w, live, got, stripANSI(ln))
				}
			}
		}
	}
}

// Raw mode clears OPOST, so the live view must use CRLF or the cursor stalls and
// the frame staircases.
func TestLiveFrameUsesCRLF(t *testing.T) {
	out := frame(&config.Config{}, testSnapshot(), nil, 100, 0, true, false, true, stats.Filter{})
	if !strings.Contains(out, "\x1b[H") || !strings.Contains(out, "\x1b[J") {
		t.Fatalf("live frame missing cursor home/clear")
	}
	for i := 0; i < len(out); i++ {
		if out[i] == '\n' && (i == 0 || out[i-1] != '\r') {
			t.Fatalf("bare LF at byte %d: raw mode cancels the carriage return", i)
		}
	}
}

// A frame taller than the screen scrolls the alternate buffer and desyncs the
// repaint, so the recent feed is capped to the terminal height.
func TestFrameHeightCap(t *testing.T) {
	snap := testSnapshot()
	for i := 0; i < 40; i++ {
		snap.Recent = append(snap.Recent, telemetry.Record{
			Time: time.Now(), Role: "worker", RoutedModel: "m", Provider: "p", Status: 200,
		})
	}
	uncapped := frame(&config.Config{}, snap, nil, 100, 0, true, false, false, stats.Filter{})
	capped := frame(&config.Config{}, snap, nil, 100, 30, true, false, false, stats.Filter{})
	if strings.Count(capped, "\n") >= strings.Count(uncapped, "\n") {
		t.Fatalf("height cap did nothing: capped=%d uncapped=%d",
			strings.Count(capped, "\n"), strings.Count(uncapped, "\n"))
	}
	if n := strings.Count(capped, "\n"); n > 30 {
		t.Fatalf("capped frame is %d rows, want <= 30", n)
	}
}

func TestFitLineANSI(t *testing.T) {
	if got := visibleCols(fitLine(cBold+"héllo wörld"+cReset, 5)); got != 5 {
		t.Fatalf("fitLine visible cols = %d, want 5 (%q)", got, fitLine(cBold+"héllo wörld"+cReset, 5))
	}
	if !strings.HasSuffix(fitLine(cRed+"abcdefghij"+cReset, 4), cReset) {
		t.Fatalf("truncated styled line must reset styling")
	}
}

func TestFrameRendersEfficiencyAndSessions(t *testing.T) {
	cfg := config.Default()
	now := time.Now()
	native := map[string]bool{"anthropic-native": true}
	records := []telemetry.Record{
		{Time: now, Session: "28b8e8a4-23c2-4afb-8604-de878fe83f93", Project: "/home/anunay/dev/space", Provider: "anthropic-native", InboundShape: "anthropic", Status: 200, InputTokens: 2, CacheWriteTokens: 218527, Guard: "thrash-trip"},
		{Time: now, Session: "28b8e8a4-23c2-4afb-8604-de878fe83f93", Provider: "openrouter", InboundShape: "anthropic", Status: 200, InputTokens: 400000, EstCostUSD: 0.12, ToolSearch: true},
	}
	snap := stats.AggregateWith(records, now.Add(-time.Hour), stats.Options{Native: native})
	out := stripANSI(frame(cfg, snap, nil, 120, 60, true, false, false, stats.Filter{}))
	for _, want := range []string{"saved n/a", "off-plan tokens 400.0k", "cache hit 0%", "tool-search 50%", "guard thrash-trip 1", "SESSIONS", "space", "218.5k", "thrash-trip"} {
		if !strings.Contains(out, want) {
			t.Fatalf("frame missing %q:\n%s", want, out)
		}
	}
	// With a reference price the saving is a number.
	snap = stats.AggregateWith(records, now.Add(-time.Hour), stats.Options{Native: native,
		Reference: map[string]config.Price{"anthropic": {In: 15, Out: 75}}})
	out = stripANSI(frame(cfg, snap, nil, 120, 60, true, false, false, stats.Filter{}))
	if !strings.Contains(out, "saved $5.88") {
		t.Fatalf("expected priced saving in frame:\n%s", out)
	}
}
