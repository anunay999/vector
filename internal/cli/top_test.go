package cli

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
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
