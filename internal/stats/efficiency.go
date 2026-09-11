package stats

import (
	"sort"
	"time"

	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/telemetry"
)

// Options tunes Aggregate beyond the record window.
type Options struct {
	// Native marks subscription providers; a request served elsewhere is
	// off-plan.
	Native map[string]bool
	// Recent caps the recent-requests feed (0 = unlimited).
	Recent int
	// Reference prices off-plan traffic at what its native provider would have
	// charged, keyed by inbound shape ("anthropic", "openai_chat",
	// "openai_responses"). Shapes without a reference are counted as unpriced.
	Reference map[string]config.Price
}

// ReferenceFrom builds the Options.Reference map from the native providers that
// declare a reference_price.
func ReferenceFrom(cfg *config.Config) map[string]config.Price {
	out := map[string]config.Price{}
	for _, p := range cfg.Providers {
		if p.Native && p.ReferencePrice != nil {
			out[p.InboundShape()] = *p.ReferencePrice
		}
	}
	return out
}

// Session is the per-client-session view: where the money went, how well the
// prompt cache held, and whether the guard fired.
type Session struct {
	ID               string
	Project          string
	Harness          string
	Requests         int
	OffPlan          int
	Errors           int
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	Cost             float64
	// MinColdPrompt is the smallest cold-cache prompt (input + cache_write with
	// cache_read == 0) seen on a successful request: a floor on the session's
	// fixed payload (system prompt, instructions, tool schemas). 0 = none seen.
	MinColdPrompt int
	ToolSearch    int
	// Guard is the most recent guard tag on the session ("thrash-trip", ...).
	Guard string
	Last  time.Time
}

// CacheHitPct is the share of prompt tokens served from cache.
func (s *Session) CacheHitPct() float64 {
	return cacheHitPct(s.InputTokens, s.CacheReadTokens, s.CacheWriteTokens)
}

// OffPlanPct is the share of the session's requests not served by a
// subscription provider.
func (s *Session) OffPlanPct() float64 {
	if s.Requests == 0 {
		return 0
	}
	return 100 * float64(s.OffPlan) / float64(s.Requests)
}

// cacheHitPct is cache_read over all prompt tokens (uncached input + cache read
// + cache write).
func cacheHitPct(input, read, write int) float64 {
	total := input + read + write
	if total <= 0 {
		return 0
	}
	return 100 * float64(read) / float64(total)
}

// CacheHitPct is the window's prompt-cache hit ratio.
func (s Snapshot) CacheHitPct() float64 {
	return cacheHitPct(s.InputTokens, s.CacheReadTokens, s.CacheWriteTokens)
}

// CacheHitPct is the group's prompt-cache hit ratio.
func (g *Group) CacheHitPct() float64 {
	return cacheHitPct(g.InputTokens, g.CacheReadTokens, g.CacheWriteTokens)
}

// ToolSearchPct is the share of requests that used deferred tool loading.
func (s Snapshot) ToolSearchPct() float64 {
	if s.Requests == 0 {
		return 0
	}
	return 100 * float64(s.ToolSearchRequests) / float64(s.Requests)
}

// SavingsKnown reports whether every off-plan request in the window could be
// priced against a reference. When false, SavingsUSD is a lower bound over the
// priced subset and SavingsUnpriced says how many requests it misses.
func (s Snapshot) SavingsKnown() bool {
	return s.SavingsUnpriced == 0 && s.OffPlan > 0
}

// referenceCost prices one record at a reference price.
func referenceCost(r telemetry.Record, p config.Price) float64 {
	return float64(r.InputTokens)*p.In/1e6 +
		float64(r.OutputTokens)*p.Out/1e6 +
		float64(r.CacheReadTokens)*p.CacheRead/1e6 +
		float64(r.CacheWriteTokens)*p.CacheWrite/1e6
}

// isColdRebuild reports whether a successful record rebuilt its prompt from a
// cold cache. Mirrors the gateway's thrash guard definition.
func isColdRebuild(r telemetry.Record) (int, bool) {
	if r.Status < 200 || r.Status >= 300 || r.CacheReadTokens > 0 {
		return 0, false
	}
	n := r.InputTokens + r.CacheWriteTokens
	return n, n > 0
}

// foldEfficiency folds one record into the snapshot's efficiency and session
// aggregates. It is called from Aggregate.
func (s *Snapshot) foldEfficiency(r telemetry.Record, opts Options) {
	s.CacheReadTokens += r.CacheReadTokens
	s.CacheWriteTokens += r.CacheWriteTokens
	if r.ToolSearch {
		s.ToolSearchRequests++
	}
	if r.Guard != "" {
		if s.GuardEvents == nil {
			s.GuardEvents = map[string]int{}
		}
		s.GuardEvents[r.Guard]++
	}
	offPlan := r.Provider != "" && !opts.Native[r.Provider]
	if offPlan && r.Status >= 200 && r.Status < 300 {
		s.OffPlanInputTokens += r.InputTokens + r.CacheReadTokens + r.CacheWriteTokens
		s.OffPlanOutputTokens += r.OutputTokens
		if p, ok := opts.Reference[r.InboundShape]; ok {
			s.SavingsUSD += referenceCost(r, p) - r.EstCostUSD
			s.SavingsPriced++
		} else {
			s.SavingsUnpriced++
		}
	}
	if r.Session == "" {
		return
	}
	if s.BySession == nil {
		s.BySession = map[string]*Session{}
	}
	ses := s.BySession[r.Session]
	if ses == nil {
		ses = &Session{ID: r.Session}
		s.BySession[r.Session] = ses
	}
	ses.Requests++
	ses.InputTokens += r.InputTokens
	ses.OutputTokens += r.OutputTokens
	ses.CacheReadTokens += r.CacheReadTokens
	ses.CacheWriteTokens += r.CacheWriteTokens
	ses.Cost += r.EstCostUSD
	if offPlan {
		ses.OffPlan++
	}
	if r.Status >= 400 || r.Error != "" {
		ses.Errors++
	}
	if r.ToolSearch {
		ses.ToolSearch++
	}
	if n, ok := isColdRebuild(r); ok && (ses.MinColdPrompt == 0 || n < ses.MinColdPrompt) {
		ses.MinColdPrompt = n
	}
	if !r.Time.Before(ses.Last) {
		ses.Last = r.Time
		if r.Project != "" {
			ses.Project = r.Project
		}
		if r.Harness != "" {
			ses.Harness = r.Harness
		}
		if r.Guard != "" {
			ses.Guard = r.Guard
		}
	}
}

// SortedSessions returns sessions ordered by cost desc, then requests desc.
func SortedSessions(m map[string]*Session) []*Session {
	out := make([]*Session, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		return out[i].ID < out[j].ID
	})
	return out
}
