// Package budget enforces spend ceilings and concurrency limits. It is the
// component that protects a user's frontier subscription from being drained by a
// runaway fan-out of subagents.
package budget

import (
	"errors"
	"sync"
	"time"
)

// ErrBudgetExceeded is returned when the breach policy is "stop" and a ceiling
// has been reached.
var ErrBudgetExceeded = errors.New("budget: daily spend ceiling reached")

// Governor tracks in-memory spend and per-harness concurrency.
type Governor struct {
	mu          sync.Mutex
	day         string
	spend       map[string]float64 // provider -> usd today
	total       float64
	dailyCap    float64
	perProvider map[string]float64
	onBreach    string

	semMu   sync.Mutex
	sems    map[string]chan struct{}
	maxConc int

	now func() time.Time
}

// New constructs a Governor. A zero dailyCap and empty perProvider disable the
// corresponding ceilings.
func New(dailyCap float64, perProvider map[string]float64, onBreach string, maxConcurrentPerHarness int) *Governor {
	if onBreach == "" {
		onBreach = "downgrade"
	}
	if maxConcurrentPerHarness <= 0 {
		maxConcurrentPerHarness = 8
	}
	caps := make(map[string]float64, len(perProvider))
	for k, v := range perProvider {
		caps[k] = v
	}
	return &Governor{
		day:         today(),
		spend:       map[string]float64{},
		dailyCap:    dailyCap,
		perProvider: caps,
		onBreach:    onBreach,
		sems:        map[string]chan struct{}{},
		maxConc:     maxConcurrentPerHarness,
		now:         time.Now,
	}
}

// Decision is the outcome of a pre-flight budget check.
type Decision struct {
	// Downgrade asks the caller to route one tier cheaper.
	Downgrade bool
	// Queue asks the caller to retry shortly or shed load with Retry-After.
	Queue bool
	// Exceeded is true when the request cannot proceed under the "stop" policy.
	Exceeded bool
}

// Breached reports whether any ceiling is exceeded.
func (d Decision) Breached() bool { return d.Downgrade || d.Queue || d.Exceeded }

// Check evaluates projected spend against the global and per-provider ceilings.
func (g *Governor) Check(provider string, estCost float64) Decision {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rolloverLocked()

	breached := false
	if g.dailyCap > 0 && g.total+estCost > g.dailyCap {
		breached = true
	}
	if cap, ok := g.perProvider[provider]; ok && cap > 0 && g.spend[provider]+estCost > cap {
		breached = true
	}
	if !breached {
		return Decision{}
	}
	switch g.onBreach {
	case "stop":
		return Decision{Exceeded: true}
	case "queue":
		return Decision{Queue: true}
	default: // downgrade
		return Decision{Downgrade: true}
	}
}

// Commit records actual spend after a request completes.
func (g *Governor) Commit(provider string, cost float64) {
	if cost <= 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rolloverLocked()
	g.spend[provider] += cost
	g.total += cost
}

// Spend returns total and per-provider spend for today.
func (g *Governor) Spend() (total float64, perProvider map[string]float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rolloverLocked()
	out := make(map[string]float64, len(g.spend))
	for k, v := range g.spend {
		out[k] = v
	}
	return g.total, out
}

// Acquire reserves a concurrency slot for a harness. It does not block: it
// returns an error immediately when the limit is reached so a caller can shed
// load with a Retry-After rather than tie up a request.
func (g *Governor) Acquire(harness string) (func(), error) {
	if harness == "" {
		harness = "unknown"
	}
	g.semMu.Lock()
	sem, ok := g.sems[harness]
	if !ok {
		sem = make(chan struct{}, g.maxConc)
		g.sems[harness] = sem
	}
	g.semMu.Unlock()

	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	default:
		return nil, ErrConcurrency
	}
}

// ErrConcurrency is returned when a harness has too many in-flight subagents.
var ErrConcurrency = errors.New("budget: too many concurrent subagents")

func (g *Governor) rolloverLocked() {
	t := g.now().Format("2006-01-02")
	if t != g.day {
		g.day = t
		g.spend = map[string]float64{}
		g.total = 0
	}
}

func today() string { return time.Now().Format("2006-01-02") }
