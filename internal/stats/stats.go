// Package stats aggregates telemetry into the shapes the CLI needs: totals,
// per-role/provider/model groups, latency percentiles, off-plan share, and a
// recent-requests feed. Both `vector spend` and `vector top` build on it.
package stats

import (
	"sort"
	"strings"
	"time"

	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/telemetry"
)

// Group is an aggregate for one dimension value (a role, provider, or model).
type Group struct {
	Requests     int
	InputTokens  int
	OutputTokens int
	Cost         float64
	Errors       int
	Latencies    []int64
}

// Add folds one record into the group.
func (g *Group) Add(r telemetry.Record) {
	g.Requests++
	g.InputTokens += r.InputTokens
	g.OutputTokens += r.OutputTokens
	g.Cost += r.EstCostUSD
	if r.Status >= 400 || r.Error != "" {
		g.Errors++
	}
	g.Latencies = append(g.Latencies, r.LatencyMS)
}

// P50 returns the median latency in milliseconds.
func (g *Group) P50() int64 { return percentile(g.Latencies, 0.50) }

// P95 returns the 95th-percentile latency in milliseconds.
func (g *Group) P95() int64 { return percentile(g.Latencies, 0.95) }

// TokensPerSec is the group's output throughput: output tokens over summed
// request latency.
func (g *Group) TokensPerSec() float64 {
	var totalMS int64
	for _, l := range g.Latencies {
		totalMS += l
	}
	if totalMS <= 0 {
		return 0
	}
	return float64(g.OutputTokens) / (float64(totalMS) / 1000)
}

// Bucket is one time slice of activity.
type Bucket struct {
	Time         time.Time
	Requests     int
	Cost         float64
	InputTokens  int
	OutputTokens int
	Errors       int
}

// BucketCount and BucketDur define the sparkline window: the last 30 minutes,
// one bucket per minute.
const (
	BucketCount = 30
	BucketDur   = time.Minute
)

// Snapshot is an aggregate over a time window.
type Snapshot struct {
	Since          time.Time
	Generated      time.Time
	Requests       int
	InputTokens    int
	OutputTokens   int
	Cost           float64
	Errors         int
	OffPlan        int
	Fallbacks      int
	LastMinute     int
	TotalLatencyMS int64
	ByRole         map[string]*Group
	ByProvider     map[string]*Group
	ByModel        map[string]*Group
	// RoleModel maps a role to the model that served it most often.
	RoleModel map[string]string
	// Buckets is a fixed-width, oldest-first time series for sparklines.
	Buckets []Bucket
	Recent  []telemetry.Record
}

// Tokens returns the total token count.
func (s Snapshot) Tokens() int { return s.InputTokens + s.OutputTokens }

// TokensPerSec is the overall output throughput: output tokens over summed
// request latency.
func (s Snapshot) TokensPerSec() float64 {
	if s.TotalLatencyMS <= 0 {
		return 0
	}
	return float64(s.OutputTokens) / (float64(s.TotalLatencyMS) / 1000)
}

// PerMinute approximates the request rate over the last minute.
func (s Snapshot) PerMinute() float64 { return float64(s.LastMinute) }

// OffPlanPct is the share of requests not served by a native (subscription)
// provider, as a percentage.
func (s Snapshot) OffPlanPct() float64 {
	if s.Requests == 0 {
		return 0
	}
	return 100 * float64(s.OffPlan) / float64(s.Requests)
}

// Filter narrows the records a snapshot is built from. Empty fields match all.
type Filter struct {
	Role     string
	Provider string
	Model    string
}

func (f Filter) active() bool {
	return f.Role != "" || f.Provider != "" || f.Model != ""
}

// Collect reads telemetry for the window and aggregates it, optionally filtered.
func Collect(cfg *config.Config, since time.Duration, recent int, f Filter) (Snapshot, error) {
	rec := telemetry.New(cfg.TelemetryDir(), cfg.Telemetry.Enabled)
	start := time.Now().Add(-since)
	records, err := rec.ReadSince(start)
	if err != nil {
		return Snapshot{}, err
	}
	if f.active() {
		filtered := make([]telemetry.Record, 0, len(records))
		for _, r := range records {
			if f.Role != "" && r.Role != f.Role {
				continue
			}
			if f.Provider != "" && r.Provider != f.Provider {
				continue
			}
			if f.Model != "" && r.RoutedModel != f.Model {
				continue
			}
			filtered = append(filtered, r)
		}
		records = filtered
	}
	native := map[string]bool{}
	for _, p := range cfg.Providers {
		if p.Native {
			native[p.ID] = true
		}
	}
	return Aggregate(records, start, native, recent), nil
}

// Aggregate computes a snapshot from records.
func Aggregate(records []telemetry.Record, since time.Time, nativeProviders map[string]bool, recent int) Snapshot {
	s := Snapshot{
		Since:      since,
		Generated:  time.Now(),
		ByRole:     map[string]*Group{},
		ByProvider: map[string]*Group{},
		ByModel:    map[string]*Group{},
		RoleModel:  map[string]string{},
	}
	roleModel := map[string]map[string]int{}
	now := time.Now()
	bucketStart := now.Truncate(BucketDur).Add(-time.Duration(BucketCount-1) * BucketDur)
	buckets := make([]Bucket, BucketCount)
	for i := range buckets {
		buckets[i].Time = bucketStart.Add(time.Duration(i) * BucketDur)
	}
	for _, r := range records {
		s.Requests++
		s.InputTokens += r.InputTokens
		s.OutputTokens += r.OutputTokens
		s.Cost += r.EstCostUSD
		s.TotalLatencyMS += r.LatencyMS
		if r.Status >= 400 || r.Error != "" {
			s.Errors++
		}
		if r.Provider != "" && !nativeProviders[r.Provider] {
			s.OffPlan++
		}
		if strings.Contains(r.Reason, "fallback") {
			s.Fallbacks++
		}
		if idx := int(r.Time.Sub(bucketStart) / BucketDur); idx >= 0 && idx < BucketCount {
			bk := &buckets[idx]
			bk.Requests++
			bk.Cost += r.EstCostUSD
			bk.InputTokens += r.InputTokens
			bk.OutputTokens += r.OutputTokens
			if r.Status >= 400 || r.Error != "" {
				bk.Errors++
			}
		}
		add(s.ByRole, r.Role, r)
		add(s.ByProvider, r.Provider, r)
		add(s.ByModel, r.RoutedModel, r)
		if r.Role != "" && r.RoutedModel != "" {
			m := roleModel[r.Role]
			if m == nil {
				m = map[string]int{}
				roleModel[r.Role] = m
			}
			m[r.RoutedModel]++
		}
	}
	s.Buckets = buckets
	s.LastMinute = buckets[BucketCount-1].Requests
	for role, models := range roleModel {
		best, bestN := "", -1
		for model, n := range models {
			if n > bestN || (n == bestN && model < best) {
				best, bestN = model, n
			}
		}
		s.RoleModel[role] = best
	}

	recentCopy := append([]telemetry.Record(nil), records...)
	sort.Slice(recentCopy, func(i, j int) bool { return recentCopy[i].Time.After(recentCopy[j].Time) })
	if recent > 0 && len(recentCopy) > recent {
		recentCopy = recentCopy[:recent]
	}
	s.Recent = recentCopy
	return s
}

func add(m map[string]*Group, key string, r telemetry.Record) {
	if key == "" {
		key = "(none)"
	}
	g := m[key]
	if g == nil {
		g = &Group{}
		m[key] = g
	}
	g.Add(r)
}

// percentile returns the p-quantile of vals (0..1) using nearest-rank.
func percentile(vals []int64, p float64) int64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := append([]int64(nil), vals...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(p * float64(len(sorted)-1))
	return sorted[idx]
}

// SortedKeys returns the keys of a group map ordered by request count desc.
func SortedKeys(m map[string]*Group) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]].Requests != m[keys[j]].Requests {
			return m[keys[i]].Requests > m[keys[j]].Requests
		}
		return keys[i] < keys[j]
	})
	return keys
}
