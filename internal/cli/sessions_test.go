package cli

import (
	"testing"
	"time"

	"github.com/anunay999/vector/internal/telemetry"
)

func TestAggregateSessions(t *testing.T) {
	now := time.Now()
	recs := []telemetry.Record{
		{Time: now.Add(-3 * time.Minute), Session: "s1", Project: "/home/anunay/dev/space2", Harness: "claude-code", Role: "architect", Provider: "anthropic-native", EstCostUSD: 0},
		{Time: now.Add(-2 * time.Minute), Session: "s1", Project: "/home/anunay/dev/space2", Harness: "claude-code", Role: "worker", Provider: "openrouter", EstCostUSD: 0.001},
		{Time: now.Add(-1 * time.Minute), Session: "s2", Project: "/home/anunay/dev/space3", Harness: "claude-code", Role: "scout", Provider: "openrouter", EstCostUSD: 0.002},
		{Time: now, Session: "", Project: "/home/anunay/dev/space", Harness: "claude-code", Role: "architect", Provider: "anthropic-native"},
	}
	rows := aggregateSessions(recs)
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	// Most recent first: the unknown session (last), then s2, then s1.
	if rows[0].Session != "" || rows[0].Checkout != "space" {
		t.Fatalf("row0 = %+v, want unknown session on space", rows[0])
	}
	if rows[0].Last.Before(rows[1].Last) {
		t.Fatal("rows not sorted by last-seen descending")
	}
	bySession := map[string]sessionRow{}
	for _, r := range rows {
		bySession[r.Session] = r
	}
	if got := bySession["s1"].Requests; got != 2 {
		t.Fatalf("s1 requests = %d, want 2", got)
	}
	if got := bySession["s1"].Cost; got < 0.0009 || got > 0.0011 {
		t.Fatalf("s1 cost = %f, want ~0.001", got)
	}
	if bySession["s1"].Roles == "" || bySession["s1"].Providers == "" {
		t.Fatalf("s1 roles/providers not summarised: %+v", bySession["s1"])
	}
}
