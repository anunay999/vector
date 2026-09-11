package config

import "testing"

func TestCanonicalModel(t *testing.T) {
	cases := map[string]string{
		"claude-opus-5":                 "claude-opus-5",
		"claude-opus-5[1m]":             "claude-opus-5",
		"anthropic/claude-opus-4.6[1m]": "claude-opus-4-6",
		"claude-sonnet-4-5-20250929":    "claude-sonnet-4-5",
		"claude-3-5-sonnet-latest":      "claude-3-5-sonnet",
		"gpt-5.4-mini":                  "gpt-5-4-mini",
		"openai/gpt-6-astra:batch":      "gpt-6-astra",
		"Opus":                          "claude-opus-5",
		"opusplan":                      "claude-opus-5",
		"vector-worker":                 "vector-worker",
		"deepseek/deepseek-v4.1-flash":  "deepseek-v4-1-flash",
	}
	for in, want := range cases {
		if got := CanonicalModel(in); got != want {
			t.Errorf("CanonicalModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReferenceLookupAndOverride(t *testing.T) {
	cfg := Default()
	p, ok := cfg.ReferencePriceFor("claude-opus-5[1m]")
	if !ok || p.In != 5 || p.Out != 25 || p.CacheRead != 0.5 || p.CacheWrite != 6.25 {
		t.Fatalf("opus 5 = %+v ok=%v", p, ok)
	}
	if p, ok := cfg.ReferencePriceFor("gpt-5.4"); !ok || p.In != 2.5 {
		t.Fatalf("gpt-5.4 = %+v ok=%v", p, ok)
	}
	if _, ok := cfg.ReferencePriceFor("vector-worker"); ok {
		t.Fatal("a virtual role must not resolve to a list price")
	}
	if _, ok := cfg.ReferencePriceFor("deepseek/deepseek-v4.1-flash"); ok {
		t.Fatal("cheap-pool models are not reference-priced")
	}

	// Overrides win and accept any wire form as the key.
	cfg.ReferencePrices = map[string]Price{"claude-opus-5[1m]": {In: 7, Out: 35}}
	if p, _ := cfg.ReferencePriceFor("anthropic/claude-opus-5"); p.In != 7 {
		t.Fatalf("override not applied: %+v", p)
	}
	rows := cfg.ReferenceTable()
	found := false
	for _, r := range rows {
		if r.Model == "claude-opus-5" {
			found = true
			if r.Source != "override" || r.Price.In != 7 {
				t.Fatalf("table row = %+v", r)
			}
		}
	}
	if !found || len(rows) < 40 {
		t.Fatalf("table incomplete: found=%v rows=%d", found, len(rows))
	}
}

func TestProviderReferencePrice(t *testing.T) {
	cfg := Default()
	anth, _ := cfg.ProviderByID("anthropic-native")
	if p, ok := cfg.ProviderReferencePrice(anth); !ok || p.In != 5 {
		t.Fatalf("anthropic-native should default to claude-opus-5's list price, got %+v ok=%v", p, ok)
	}
	oai, _ := cfg.ProviderByID("openai-native")
	if p, ok := cfg.ProviderReferencePrice(oai); !ok || p.In != 10 {
		t.Fatalf("openai-native should default to gpt-6-astra's list price, got %+v ok=%v", p, ok)
	}
	anth.ReferencePrice = &Price{In: 1, Out: 2}
	if p, _ := cfg.ProviderReferencePrice(anth); p.In != 1 {
		t.Fatal("explicit reference_price must win")
	}
	if _, ok := cfg.ProviderReferencePrice(Provider{ID: "x", Native: true}); ok {
		t.Fatal("no default_model and no reference_price must be unpriced")
	}
}
