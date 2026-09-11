package stats

import (
	"testing"
	"time"

	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/telemetry"
)

func TestDailySeries(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	recs := []telemetry.Record{
		{Time: today, EstCostUSD: 0.10, InputTokens: 10, OutputTokens: 5},
		{Time: today.AddDate(0, 0, -1), EstCostUSD: 0.02},
		{Time: today.AddDate(0, 0, -30), EstCostUSD: 9.99}, // outside the window
	}
	days := dailySeries(recs, 3)
	if len(days) != 3 {
		t.Fatalf("len = %d, want 3", len(days))
	}
	last := days[2]
	if last.Requests != 1 || last.Cost != 0.10 || last.Tokens != 15 {
		t.Fatalf("today bucket = %+v", last)
	}
	if days[1].Requests != 1 {
		t.Fatalf("yesterday bucket = %+v", days[1])
	}
	if days[0].Requests != 0 {
		t.Fatalf("old day should be empty: %+v", days[0])
	}
}

func TestAggregate(t *testing.T) {
	now := time.Now()
	recs := []telemetry.Record{
		{Time: now, Role: "worker", Provider: "openrouter", RoutedModel: "glm", InputTokens: 10, OutputTokens: 5, EstCostUSD: 0.001, Status: 200, LatencyMS: 100, Reason: "role worker"},
		{Time: now, Role: "architect", Provider: "anthropic-native", RoutedModel: "claude-opus-5", Status: 200, LatencyMS: 200},
		{Time: now, Role: "worker", Provider: "openrouter", RoutedModel: "glm", Status: 200, LatencyMS: 300, Reason: "fallback -> worker"},
	}
	native := map[string]bool{"anthropic-native": true}
	s := Aggregate(recs, now.Add(-time.Hour), native, 10)

	if s.Requests != 3 {
		t.Fatalf("requests = %d, want 3", s.Requests)
	}
	if s.OffPlan != 2 {
		t.Fatalf("off-plan = %d, want 2", s.OffPlan)
	}
	if s.Fallbacks != 1 {
		t.Fatalf("fallbacks = %d, want 1", s.Fallbacks)
	}
	if got := s.ByRole["worker"].Requests; got != 2 {
		t.Fatalf("worker requests = %d, want 2", got)
	}
	if s.RoleModel["worker"] != "glm" {
		t.Fatalf("role model = %q, want glm", s.RoleModel["worker"])
	}
	if pct := s.OffPlanPct(); pct < 66 || pct > 67 {
		t.Fatalf("off-plan pct = %.1f, want ~66.7", pct)
	}
	if s.Tokens() != 15 {
		t.Fatalf("tokens = %d, want 15", s.Tokens())
	}
}

func TestEfficiencyAggregates(t *testing.T) {
	now := time.Now()
	native := map[string]bool{"anthropic-native": true}
	ref := map[string]config.Price{"anthropic": {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75}}
	records := []telemetry.Record{
		// Subscription turn: cold start, then a cache hit. Not off-plan.
		{Time: now.Add(-9 * time.Minute), Session: "s1", Project: "/home/u/dev/space", Harness: "claude-code", Provider: "anthropic-native", InboundShape: "anthropic", Status: 200, InputTokens: 2, CacheWriteTokens: 250000},
		{Time: now.Add(-8 * time.Minute), Session: "s1", Provider: "anthropic-native", InboundShape: "anthropic", Status: 200, InputTokens: 2, CacheReadTokens: 250000, CacheWriteTokens: 3000, ToolSearch: true},
		// Routed subagent turn: 1M input tokens at deepseek prices ($0.30/M) that
		// would have cost $15/M at the reference.
		{Time: now.Add(-7 * time.Minute), Session: "s1", Provider: "openrouter", InboundShape: "anthropic", Status: 200, InputTokens: 1_000_000, OutputTokens: 1000, EstCostUSD: 0.3012, Guard: "thrash-trip"},
		// Off-plan on a shape with no reference: counted, not priced.
		{Time: now.Add(-6 * time.Minute), Session: "s2", Provider: "openrouter", InboundShape: "openai_chat", Status: 200, InputTokens: 1000, EstCostUSD: 0.001},
		// A smaller cold rebuild later in s1 lowers its floor.
		{Time: now.Add(-5 * time.Minute), Session: "s1", Provider: "anthropic-native", InboundShape: "anthropic", Status: 200, InputTokens: 2, CacheWriteTokens: 90000},
		// Failed requests never count as a cold rebuild.
		{Time: now.Add(-4 * time.Minute), Session: "s1", Provider: "anthropic-native", InboundShape: "anthropic", Status: 429, InputTokens: 0, CacheWriteTokens: 0, Error: "rate limited"},
		// Nor does a tiny cold prompt (a probe, or a pre-cache_write record).
		{Time: now.Add(-3 * time.Minute), Session: "s1", Provider: "anthropic-native", InboundShape: "anthropic", Status: 200, InputTokens: 2},
	}
	s := AggregateWith(records, now.Add(-time.Hour), Options{Native: native, Reference: ref})

	if s.OffPlan != 2 || s.SavingsPriced != 1 || s.SavingsUnpriced != 1 {
		t.Fatalf("off-plan=%d priced=%d unpriced=%d", s.OffPlan, s.SavingsPriced, s.SavingsUnpriced)
	}
	// reference: 1M×15 + 1000×75 = $15.075 minus actual $0.3012
	if want := 15.075 - 0.3012; s.SavingsUSD < want-1e-6 || s.SavingsUSD > want+1e-6 {
		t.Fatalf("savings = %.4f, want %.4f", s.SavingsUSD, want)
	}
	if s.SavingsKnown() {
		t.Fatal("savings must not be 'known' while an off-plan request is unpriced")
	}
	if s.OffPlanInputTokens != 1_001_000 || s.OffPlanOutputTokens != 1000 {
		t.Fatalf("off-plan tokens = %d/%d", s.OffPlanInputTokens, s.OffPlanOutputTokens)
	}
	if s.ToolSearchRequests != 1 || s.GuardEvents["thrash-trip"] != 1 {
		t.Fatalf("tool_search=%d guard=%v", s.ToolSearchRequests, s.GuardEvents)
	}
	// cache hit = read / (input + read + write)
	input, read, write := s.InputTokens, s.CacheReadTokens, s.CacheWriteTokens
	if want := 100 * float64(read) / float64(input+read+write); s.CacheHitPct() != want {
		t.Fatalf("cache hit = %.2f, want %.2f", s.CacheHitPct(), want)
	}

	s1 := s.BySession["s1"]
	if s1 == nil || s1.Requests != 6 || s1.OffPlan != 1 || s1.Errors != 1 {
		t.Fatalf("s1 = %+v", s1)
	}
	if s1.MinColdPrompt != 90002 {
		t.Fatalf("s1 cold floor = %d, want 90002", s1.MinColdPrompt)
	}
	if s1.Project != "/home/u/dev/space" || s1.Harness != "claude-code" || s1.Guard != "thrash-trip" {
		t.Fatalf("s1 identity/guard = %q %q %q", s1.Project, s1.Harness, s1.Guard)
	}
	if sorted := SortedSessions(s.BySession); sorted[0].ID != "s1" || sorted[1].ID != "s2" {
		t.Fatalf("sessions not ordered by cost: %s, %s", sorted[0].ID, sorted[1].ID)
	}
	// Sessionless records are excluded from BySession.
	s = AggregateWith([]telemetry.Record{{Time: now, Provider: "openrouter", Status: 200}}, now.Add(-time.Hour), Options{Native: native})
	if len(s.BySession) != 0 {
		t.Fatal("sessionless record created a session")
	}
}

func TestReferenceFrom(t *testing.T) {
	cfg := config.Default()
	if got := ReferenceFrom(cfg); len(got) != 0 {
		t.Fatalf("default config has no reference prices, got %v", got)
	}
	cfg.Providers[1].ReferencePrice = &config.Price{In: 15, Out: 75}
	got := ReferenceFrom(cfg)
	if p, ok := got["anthropic"]; !ok || p.In != 15 {
		t.Fatalf("reference = %v", got)
	}
}
