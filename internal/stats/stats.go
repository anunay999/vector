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
	Requests         int
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	Cost             float64
	Errors           int
	Latencies        []int64
}

// Add folds one record into the group.
func (g *Group) Add(r telemetry.Record) {
	g.Requests++
	g.InputTokens += r.InputTokens
	g.OutputTokens += r.OutputTokens
	g.CacheReadTokens += r.CacheReadTokens
	g.CacheWriteTokens += r.CacheWriteTokens
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

// HistogramDays is how many days the daily cost histogram covers.
const HistogramDays = 14

// Day is one calendar day of activity.
type Day struct {
	Date     time.Time
	Requests int
	Cost     float64
	Tokens   int
}

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
	// Daily is a 14-day, oldest-first series for the cost histogram.
	Daily  []Day
	Recent []telemetry.Record

	// Efficiency: what routing bought, and how the prompt cache held.
	CacheReadTokens     int
	CacheWriteTokens    int
	ToolSearchRequests  int
	OffPlanInputTokens  int // prompt tokens kept off the subscription
	OffPlanOutputTokens int
	// SavingsUSD is Σ(reference price − actual est. cost) over the off-plan
	// requests whose inbound shape has a reference price. SavingsPriced /
	// SavingsUnpriced count the requests that were / were not priced.
	SavingsUSD      float64
	SavingsPriced   int
	SavingsUnpriced int
	// GuardEvents counts circuit-breaker tags ("thrash-trip", ...).
	GuardEvents map[string]int
	// BySession groups by client session id; sessionless requests are omitted.
	BySession map[string]*Session
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
// It reads at least HistogramDays so the daily series is always complete.
func Collect(cfg *config.Config, since time.Duration, recent int, f Filter) (Snapshot, error) {
	rec := telemetry.New(cfg.TelemetryDir(), cfg.Telemetry.Enabled)
	readWindow := since
	if minWindow := time.Duration(HistogramDays) * 24 * time.Hour; readWindow < minWindow {
		readWindow = minWindow
	}
	now := time.Now()
	records, err := rec.ReadSince(now.Add(-readWindow))
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
	windowStart := now.Add(-since)
	window := records
	if since < readWindow {
		window = make([]telemetry.Record, 0, len(records))
		for _, r := range records {
			if !r.Time.Before(windowStart) {
				window = append(window, r)
			}
		}
	}
	snap := AggregateWith(window, windowStart, Options{Native: native, Recent: recent, Reference: ReferenceFrom(cfg), ModelPrice: cfg.ReferencePriceFor})
	snap.Daily = dailySeries(records, HistogramDays)
	return snap, nil
}

// dailySeries buckets records into the last `days` calendar days (local time),
// oldest first.
func dailySeries(records []telemetry.Record, days int) []Day {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	out := make([]Day, days)
	index := map[string]int{}
	for i := range out {
		d := today.AddDate(0, 0, -(days - 1 - i))
		out[i].Date = d
		index[d.Format("2006-01-02")] = i
	}
	for _, r := range records {
		i, ok := index[r.Time.In(now.Location()).Format("2006-01-02")]
		if !ok {
			continue
		}
		out[i].Requests++
		out[i].Cost += r.EstCostUSD
		out[i].Tokens += r.InputTokens + r.OutputTokens
	}
	return out
}

// Aggregate computes a snapshot from records with no reference pricing.
func Aggregate(records []telemetry.Record, since time.Time, nativeProviders map[string]bool, recent int) Snapshot {
	return AggregateWith(records, since, Options{Native: nativeProviders, Recent: recent})
}

// AggregateWith computes a snapshot from records.
func AggregateWith(records []telemetry.Record, since time.Time, opts Options) Snapshot {
	nativeProviders, recent := opts.Native, opts.Recent
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
		s.foldEfficiency(r, opts)
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
