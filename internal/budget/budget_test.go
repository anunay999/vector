package budget

import "testing"

func TestDowngradeWhenOverCap(t *testing.T) {
	g := New(1.0, nil, "downgrade", 4)
	g.Commit("openrouter", 1.5)
	if d := g.Check("openrouter", 0); !d.Downgrade {
		t.Fatal("expected downgrade decision")
	}
}

func TestStopWhenOverCap(t *testing.T) {
	g := New(1.0, nil, "stop", 4)
	g.Commit("openrouter", 1.5)
	if d := g.Check("openrouter", 0); !d.Exceeded {
		t.Fatal("expected exceeded decision")
	}
}

func TestAcquireConcurrency(t *testing.T) {
	g := New(0, nil, "downgrade", 1)
	rel, err := g.Acquire("claude-code")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := g.Acquire("claude-code"); err == nil {
		t.Fatal("expected second acquire to fail at limit")
	}
	rel()
	if _, err := g.Acquire("claude-code"); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}

func TestSpendRollup(t *testing.T) {
	g := New(0, nil, "downgrade", 4)
	g.Commit("openrouter", 0.25)
	g.Commit("openrouter", 0.25)
	g.Commit("baseten", 1.0)
	total, per := g.Spend()
	if total != 1.5 {
		t.Fatalf("total = %v, want 1.5", total)
	}
	if per["openrouter"] != 0.5 {
		t.Fatalf("openrouter = %v, want 0.5", per["openrouter"])
	}
}

func TestPerProviderCap(t *testing.T) {
	g := New(10, map[string]float64{"openrouter": 1.0}, "downgrade", 4)
	g.Commit("openrouter", 1.5)
	if d := g.Check("openrouter", 0); !d.Downgrade {
		t.Fatal("expected per-provider downgrade")
	}
	if d := g.Check("baseten", 0); d.Breached() {
		t.Fatal("unrelated provider should be unaffected")
	}
}
