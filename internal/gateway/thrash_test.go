package gateway

import (
	"testing"
	"time"

	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/llm"
)

func newTestGuard(action string) (*thrashGuard, *time.Time) {
	cfg := config.DefaultThrash()
	cfg.Action = action
	g := newThrashGuard(cfg)
	now := time.Date(2026, 9, 11, 5, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return now }
	return g, &now
}

// The transcript this guard was built from: a 1M session compacted at 05:09,
// then every post-compaction request rebuilt a ~220k-290k prompt from a cold
// cache and compacted again within a turn.
var (
	coldRebuild  = llm.Usage{InputTokens: 2, CacheWriteTokens: 218527}
	warmTurn     = llm.Usage{InputTokens: 2, CacheReadTokens: 641790, CacheWriteTokens: 3139}
	smallColdSt  = llm.Usage{InputTokens: 2, CacheWriteTokens: 40000}
	sessionThras = "28b8e8a4-23c2-4afb-8604-de878fe83f93"
)

func TestThrashGuardTripsOnRepeatedColdRebuilds(t *testing.T) {
	g, now := newTestGuard(config.ThrashWarn)

	if tripped, _ := g.observe(sessionThras, coldRebuild); tripped {
		t.Fatal("first cold rebuild (session start) must not trip")
	}
	*now = now.Add(3 * time.Minute)
	tripped, n := g.observe(sessionThras, coldRebuild)
	if !tripped || n != 2 {
		t.Fatalf("second cold rebuild inside window should trip: tripped=%v n=%d", tripped, n)
	}
	v := g.check(sessionThras)
	if !v.Tripped || v.Block {
		t.Fatalf("warn mode verdict = %+v", v)
	}
	if v.Detail == "" {
		t.Fatal("verdict needs an explanation")
	}

	// Another session is unaffected.
	if v := g.check("other"); v.Tripped {
		t.Fatal("guard leaked across sessions")
	}

	// Cooldown resets the session.
	*now = now.Add(6 * time.Minute)
	if v := g.check(sessionThras); v.Tripped {
		t.Fatalf("expected reset after cooldown, got %+v", v)
	}
}

func TestThrashGuardIgnoresNormalTraffic(t *testing.T) {
	g, now := newTestGuard(config.ThrashBlock)

	// Session start, then long-lived cache hits, then one compaction hours later.
	g.observe(sessionThras, coldRebuild)
	for i := 0; i < 20; i++ {
		*now = now.Add(time.Minute)
		if tripped, _ := g.observe(sessionThras, warmTurn); tripped {
			t.Fatal("warm turns must never trip")
		}
	}
	*now = now.Add(3 * time.Hour)
	if tripped, _ := g.observe(sessionThras, coldRebuild); tripped {
		t.Fatal("a single compaction hours after start is normal")
	}
	// Small cold prompts (a fresh short session, a resume) never count.
	for i := 0; i < 5; i++ {
		*now = now.Add(time.Second)
		if tripped, _ := g.observe("short", smallColdSt); tripped {
			t.Fatal("small cold prompts must not count as rebuilds")
		}
	}
	// No session id: nothing to key on.
	if tripped, _ := g.observe("", coldRebuild); tripped {
		t.Fatal("sessionless requests must not trip")
	}
}

func TestThrashGuardBlockModeAndDisable(t *testing.T) {
	g, now := newTestGuard(config.ThrashBlock)
	g.observe(sessionThras, coldRebuild)
	*now = now.Add(time.Minute)
	g.observe(sessionThras, coldRebuild)
	if v := g.check(sessionThras); !v.Tripped || !v.Block {
		t.Fatalf("block mode verdict = %+v", v)
	}

	off := config.DefaultThrash()
	off.Enabled = new(bool)
	g.reconfigure(off)
	if v := g.check(sessionThras); v.Tripped {
		t.Fatal("disabled guard must not report trips")
	}
}
