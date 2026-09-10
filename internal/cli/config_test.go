package cli

import "testing"

func TestCoerce(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"true", true},
		{"false", false},
		{"25", 25},
		{"1.5", 1.5},
		{"hello", "hello"},
		{"[a,b]", []any{"a", "b"}},
	}
	for _, c := range cases {
		got := coerce(c.in)
		switch want := c.want.(type) {
		case []any:
			g, ok := got.([]any)
			if !ok || len(g) != len(want) {
				t.Fatalf("coerce(%q) = %#v, want %#v", c.in, got, c.want)
			}
		default:
			if got != c.want {
				t.Fatalf("coerce(%q) = %#v, want %#v", c.in, got, c.want)
			}
		}
	}
}

func TestGetSetPath(t *testing.T) {
	m := map[string]any{
		"budget": map[string]any{"daily_usd": 25},
		"roles": map[string]any{
			"worker": map[string]any{"tier": "cheap"},
		},
	}
	if err := setPath(m, []string{"budget", "daily_usd"}, 10); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := setPath(m, []string{"roles", "worker", "tier"}, "smart"); err != nil {
		t.Fatalf("set: %v", err)
	}
	v, err := getPath(m, []string{"budget", "daily_usd"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v != 10 {
		t.Fatalf("got %v, want 10", v)
	}
	v, _ = getPath(m, []string{"roles", "worker", "tier"})
	if v != "smart" {
		t.Fatalf("got %v, want smart", v)
	}
	// Creating a missing intermediate map.
	if err := setPath(m, []string{"complexity", "default_floor"}, "worker"); err != nil {
		t.Fatalf("set new: %v", err)
	}
	if v, _ := getPath(m, []string{"complexity", "default_floor"}); v != "worker" {
		t.Fatalf("got %v", v)
	}
}
