package gateway

import (
	"fmt"
	"sync"
	"time"

	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/llm"
)

// thrashGuard is a per-session circuit breaker for context-compaction loops.
//
// The signature it watches for: a harness session whose requests keep arriving
// with a cold prompt cache (cache_read_tokens == 0) and a very large prompt
// (input + cache_write >= min_prompt_tokens). A healthy session pays that once
// at start and then rides the cache. Claude Code's auto-compact loop pays it on
// every turn: when the fixed payload (tool schemas, system prompt, instructions)
// is already above the compact threshold, each compaction rebuilds the whole
// prompt, the next turn is over the limit again, and the session burns
// full-price input tokens until the harness gives up. Two cold rebuilds of that
// size inside a short window is not normal use.
//
// The guard only observes 2xx responses that carried usage, and only keys on
// requests that identified their session. Side-channel endpoints never count.
type thrashGuard struct {
	mu       sync.Mutex
	cfg      config.Thrash
	sessions map[string]*thrashState
	now      func() time.Time
}

type thrashState struct {
	cold      []time.Time // recent cold, oversized rebuilds
	trippedAt time.Time   // zero until the session trips
	trips     int
}

// thrashVerdict is what proxy consults before forwarding a request.
type thrashVerdict struct {
	Tripped bool
	Block   bool
	Detail  string
}

func newThrashGuard(cfg config.Thrash) *thrashGuard {
	return &thrashGuard{cfg: cfg, sessions: map[string]*thrashState{}, now: time.Now}
}

// reconfigure swaps thresholds on hot reload without forgetting session state.
func (g *thrashGuard) reconfigure(cfg config.Thrash) {
	g.mu.Lock()
	g.cfg = cfg
	g.mu.Unlock()
}

// isColdRebuild reports whether one response's usage looks like a from-scratch
// prompt of at least min tokens.
func isColdRebuild(u llm.Usage, min int) bool {
	if u.CacheReadTokens > 0 {
		return false
	}
	return u.InputTokens+u.CacheWriteTokens >= min
}

// observe records a completed request and reports whether it tripped the
// breaker. The count returned is the number of cold rebuilds inside the window.
func (g *thrashGuard) observe(session string, u llm.Usage) (tripped bool, count int) {
	if session == "" || !g.cfg.On() {
		return false, 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	st := g.sessions[session]
	if st == nil {
		st = &thrashState{}
		g.sessions[session] = st
	}
	g.pruneLocked(now)
	if !isColdRebuild(u, g.cfg.MinPromptTokens) {
		return false, len(st.cold)
	}
	st.cold = append(st.cold, now)
	st.cold = trimWindow(st.cold, now.Add(-g.cfg.Window))
	if len(st.cold) >= g.cfg.ColdRequests && st.trippedAt.IsZero() {
		st.trippedAt = now
		st.trips++
		return true, len(st.cold)
	}
	return false, len(st.cold)
}

// check returns the current verdict for a session before its next request.
// A tripped session stays tripped for the cooldown, then resets so a session
// that recovered (for example after /clear) is not punished forever.
func (g *thrashGuard) check(session string) thrashVerdict {
	if session == "" || !g.cfg.On() {
		return thrashVerdict{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.sessions[session]
	if st == nil || st.trippedAt.IsZero() {
		return thrashVerdict{}
	}
	now := g.now()
	if now.Sub(st.trippedAt) > g.cfg.Cooldown {
		st.trippedAt = time.Time{}
		st.cold = nil
		return thrashVerdict{}
	}
	detail := fmt.Sprintf("vector: context thrash detected for this session — %d prompt rebuilds of ≥%dk tokens with a cold cache within %s. "+
		"The fixed request payload (tool schemas, system prompt) is likely above the harness's auto-compact threshold; "+
		"trim MCP servers, enable tool deferral (ENABLE_TOOL_SEARCH), or /clear. Guard resets in %s.",
		g.cfg.ColdRequests, g.cfg.MinPromptTokens/1000, g.cfg.Window,
		(g.cfg.Cooldown - now.Sub(st.trippedAt)).Round(time.Second))
	return thrashVerdict{Tripped: true, Block: g.cfg.Action == config.ThrashBlock, Detail: detail}
}

// pruneLocked forgets sessions that have been quiet for a long time so the map
// does not grow with every session ever seen.
func (g *thrashGuard) pruneLocked(now time.Time) {
	if len(g.sessions) < 256 {
		return
	}
	stale := now.Add(-24 * time.Hour)
	for id, st := range g.sessions {
		last := st.trippedAt
		if n := len(st.cold); n > 0 && st.cold[n-1].After(last) {
			last = st.cold[n-1]
		}
		if last.Before(stale) {
			delete(g.sessions, id)
		}
	}
}

func trimWindow(ts []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(ts) && ts[i].Before(cutoff) {
		i++
	}
	return ts[i:]
}
