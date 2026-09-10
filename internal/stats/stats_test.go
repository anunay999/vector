package stats

import (
	"testing"
	"time"

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
