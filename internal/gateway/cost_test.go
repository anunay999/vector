package gateway

import (
	"math"
	"testing"

	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/llm"
	"github.com/anunay999/vector/internal/router"
)

// Cache-read tokens are billed by the provider at a discount and must count
// toward the estimate, otherwise spend under-reports on cache-heavy traffic.
func TestCostOfIncludesCacheTokens(t *testing.T) {
	d := router.Decision{Price: config.Price{In: 0.30, Out: 1.20, CacheRead: 0.006, CacheWrite: 0.04}}
	u := llm.Usage{
		InputTokens:      1_000_000,
		OutputTokens:     1_000_000,
		CacheReadTokens:  1_000_000,
		CacheWriteTokens: 1_000_000,
	}
	got := costOf(d, u)
	want := 0.30 + 1.20 + 0.006 + 0.04
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("costOf = %v, want %v", got, want)
	}
}
