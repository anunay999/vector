// Package config loads, validates, and applies the vector configuration. The
// configuration is the single source of truth: providers, the model registry,
// agent roles (automatic agent routing), model redirects (explicit model
// routing), budgets, and per-harness wiring.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Defaults and conventional paths.
const (
	DirName              = "vector"
	FileName             = "config.yaml"
	EnvFileName          = "env"
	DefaultAnthropicAddr = "127.0.0.1:7331"
	DefaultOpenAIAddr    = "127.0.0.1:7331"
	DefaultAdminAddr     = "127.0.0.1:7333"
	CurrentVersion       = 1
)

// Config is the root configuration document.
type Config struct {
	Version        int                `yaml:"version"`
	RoutingEnabled bool               `yaml:"routing_enabled"`
	Listen         Listen             `yaml:"listen"`
	Providers      []Provider         `yaml:"providers"`
	Models         []Model            `yaml:"models"`
	Roles          map[string]Role    `yaml:"roles"`
	ModelMap       []ModelRule        `yaml:"model_map"`
	Subagents      Subagents          `yaml:"subagents"`
	Budget         Budget             `yaml:"budget"`
	Fallback       Fallback           `yaml:"fallback"`
	Harnesses      map[string]Harness `yaml:"harnesses"`
	Telemetry      Telemetry          `yaml:"telemetry"`

	// path is the file this config was loaded from, if any.
	path string `yaml:"-"`
}

// Listen holds the loopback addresses the gateway binds.
type Listen struct {
	Anthropic string `yaml:"anthropic"`
	OpenAI    string `yaml:"openai"`
	Admin     string `yaml:"admin"`
}

// Provider types.
const (
	ProviderOpenAICompatible = "openai_compatible"
	ProviderAnthropic        = "anthropic"
	ProviderOpenAIResponses  = "openai_responses"
)

// Provider is an upstream that can serve one or more models.
type Provider struct {
	ID string `yaml:"id"`
	// Type selects the protocol spoken upstream.
	Type string `yaml:"type"`
	// BaseURL is the OpenAI-compatible / native root.
	BaseURL string `yaml:"base_url"`
	// AnthropicBaseURL is an optional Anthropic-shape root on the same provider
	// (OpenRouter exposes one). When set, Anthropic-shape requests skip translation.
	AnthropicBaseURL string `yaml:"anthropic_base_url"`
	// APIKey may be a literal or a ${VAR} reference. Empty means "use the
	// inbound credential" (native plan passthrough).
	APIKey string `yaml:"api_key"`
	// DefaultModel is used when a virtual role routes to a native provider and
	// the inbound request did not name a real native model (for example
	// "vector-escalate" -> anthropic-native -> claude-opus-5).
	DefaultModel string `yaml:"default_model"`
	// Headers are merged into every upstream request.
	Headers map[string]string `yaml:"headers"`
	// Native marks a provider that must be called with the inbound Authorization
	// header rather than a configured key.
	Native bool `yaml:"native"`
}

// Price is per-million-token pricing in USD. CacheRead/CacheWrite are the
// provider's discounted rates for cached prompt tokens; prompt tokens that are
// not a cache hit are billed at In.
type Price struct {
	In         float64 `yaml:"in" json:"in"`
	Out        float64 `yaml:"out" json:"out"`
	CacheRead  float64 `yaml:"cache_read,omitempty" json:"cache_read,omitempty"`
	CacheWrite float64 `yaml:"cache_write,omitempty" json:"cache_write,omitempty"`
}

// Model is a registry entry describing a routable model.
type Model struct {
	// ID is the namespaced id: "<providerID>/<upstreamModel>".
	ID      string   `yaml:"id"`
	Tags    []string `yaml:"tags"`
	Context int      `yaml:"context"`
	Price   Price    `yaml:"price"`
}

// Role is a logical delegation role resolved to a concrete model at request time.
type Role struct {
	Tier        string   `yaml:"tier" json:"tier"` // frontier | smart | cheap
	Primary     bool     `yaml:"primary" json:"primary"`
	Prefer      []string `yaml:"prefer" json:"prefer"`
	Description string   `yaml:"description" json:"description"`
}

// ModelRule is one entry of the model_map redirect table: a concrete inbound
// model id is forced to another target (role, registry model, or provider),
// taking precedence over agent-mode routing but not over an explicit registry
// model or a virtual role. From supports a trailing '*' glob.
type ModelRule struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// Subagents controls agent-mode routing: when Route is unset or true, a
// structurally detected subagent request is sent to the worker agent. This is
// the only implicit routing; everything else is either an agent name or an
// explicit model_map rule.
type Subagents struct {
	Route *bool `yaml:"route"`
}

// Routes reports whether detected subagents are routed to the worker agent.
func (s Subagents) Routes() bool { return s.Route == nil || *s.Route }

// boolPtr returns a pointer to b, for tri-state config fields.
func boolPtr(b bool) *bool { return &b }

// Budget configures spend and concurrency governance.
type Budget struct {
	DailyUSD                         float64            `yaml:"daily_usd"`
	PerProvider                      map[string]float64 `yaml:"per_provider"`
	OnBreach                         string             `yaml:"on_breach"` // downgrade | queue | stop
	MaxConcurrentSubagentsPerHarness int                `yaml:"max_concurrent_subagents_per_harness"`
}

// Fallback configures resilience.
type Fallback struct {
	TTFTTimeout time.Duration `yaml:"ttft_timeout"`
}

// Harness holds per-harness enablement and defaults.
type Harness struct {
	Enabled       bool   `yaml:"enabled"`
	SubagentModel string `yaml:"subagent_model"`
	Profile       string `yaml:"profile"`
}

// Telemetry configures the local JSONL request store.
type Telemetry struct {
	Dir           string `yaml:"dir"`
	RetentionDays int    `yaml:"retention_days"`
	Enabled       bool   `yaml:"enabled"`
}

// Dir returns the vector config directory, honoring VECTOR_CONFIG_DIR.
func Dir() string {
	if d := os.Getenv("VECTOR_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "."+DirName)
	}
	return filepath.Join(home, ".config", DirName)
}

// DefaultPath returns the default config file path.
func DefaultPath() string { return filepath.Join(Dir(), FileName) }

// Path reports the file this config was loaded from.
func (c *Config) Path() string { return c.path }

// SetPath records the source path (used by tests and writers).
func (c *Config) SetPath(p string) { c.path = p }

// Default returns a complete, OpenRouter-first configuration. It is used when no
// config file exists and as the base for config init.
func Default() *Config {
	return &Config{
		Version:        CurrentVersion,
		RoutingEnabled: true,
		Listen: Listen{
			Anthropic: DefaultAnthropicAddr,
			OpenAI:    DefaultOpenAIAddr,
			Admin:     DefaultAdminAddr,
		},
		Providers: []Provider{
			{
				ID:               "openrouter",
				Type:             ProviderOpenAICompatible,
				BaseURL:          "https://openrouter.ai/api/v1",
				AnthropicBaseURL: "https://openrouter.ai/api/v1",
				APIKey:           "${OPENROUTER_API_KEY}",
			},
			{
				ID:           "anthropic-native",
				Type:         ProviderAnthropic,
				BaseURL:      "https://api.anthropic.com",
				DefaultModel: "claude-opus-5",
				Native:       true,
			},
			{
				ID:           "openai-native",
				Type:         ProviderOpenAIResponses,
				BaseURL:      "https://api.openai.com/v1",
				DefaultModel: "gpt-6-astra",
				Native:       true,
			},
		},
		Models: []Model{
			{ID: "openrouter/z-ai/glm-5.3-flash", Tags: []string{"cheap", "fast", "tools", "long_context"}, Context: 1310720, Price: Price{In: 0.15, Out: 0.50, CacheRead: 0.03}},
			{ID: "openrouter/deepseek/deepseek-v4.1-flash", Tags: []string{"cheap", "fast", "tools", "high_throughput", "long_context"}, Context: 1048576, Price: Price{In: 0.30, Out: 1.20, CacheRead: 0.006}},
			{ID: "openrouter/moonshotai/kimi-k3", Tags: []string{"smart", "tools", "long_context", "agentic"}, Context: 1048576, Price: Price{In: 2.34, Out: 11.70, CacheRead: 0.261}},
			{ID: "openrouter/z-ai/glm-5.3", Tags: []string{"smart", "reasoning", "code"}, Context: 1310720, Price: Price{In: 1.40, Out: 4.40, CacheRead: 0.26}},
			{ID: "openrouter/deepseek/deepseek-v4-pro", Tags: []string{"smart", "reasoning"}, Context: 1048576, Price: Price{In: 0.955, Out: 1.911, CacheRead: 0.079605}},
			{ID: "openrouter/google/gemini-3.8-flash", Tags: []string{"smart", "vision", "long_context", "fast"}, Context: 1048576, Price: Price{In: 0.75, Out: 3.75, CacheRead: 0.075, CacheWrite: 0.041667}},
		},
		Roles: map[string]Role{
			"architect":  {Tier: "frontier", Primary: true, Prefer: []string{"anthropic-native", "openai-native"}, Description: "Understand the issue, plan, and decompose work. Frontier only."},
			"lead":       {Tier: "frontier", Primary: true, Prefer: []string{"anthropic-native", "openrouter/z-ai/glm-5.3"}, Description: "Coordinate multi-step work and adjudicate."},
			"reviewer":   {Tier: "smart", Prefer: []string{"openrouter/moonshotai/kimi-k3", "openrouter/z-ai/glm-5.3"}, Description: "Review code, tests, and PR comments."},
			"worker":     {Tier: "cheap", Prefer: []string{"openrouter/deepseek/deepseek-v4.1-flash", "openrouter/z-ai/glm-5.3-flash"}, Description: "Scoped edits, mechanical fixes, resolving comments."},
			"scout":      {Tier: "cheap", Prefer: []string{"openrouter/z-ai/glm-5.3-flash", "openrouter/deepseek/deepseek-v4.1-flash"}, Description: "Read-only search, read, and summarize."},
			"researcher": {Tier: "smart", Prefer: []string{"openrouter/moonshotai/kimi-k3", "openrouter/google/gemini-3.8-flash"}, Description: "Long-context reading of docs and benchmarks."},
			"escalate":   {Tier: "frontier", Prefer: []string{"anthropic-native", "openai-native"}, Description: "Hard or repeated-failure tasks. Frontier only."},
		},
		Subagents: Subagents{Route: boolPtr(true)},
		Budget: Budget{
			DailyUSD:                         25,
			OnBreach:                         "downgrade",
			MaxConcurrentSubagentsPerHarness: 8,
		},
		Fallback: Fallback{
			TTFTTimeout: 30 * time.Second,
		},
		Harnesses: map[string]Harness{
			"claude-code": {Enabled: true, SubagentModel: "vector-worker"},
			"codex":       {Enabled: true, Profile: "vector"},
			"opencode":    {Enabled: true},
		},
		Telemetry: Telemetry{
			Dir:           filepath.Join(Dir(), "telemetry"),
			RetentionDays: 90,
			Enabled:       true,
		},
	}
}

// applyDefaults fills scalar fields that were omitted from a loaded file. It
// intentionally does not inject providers, models, roles, or model redirects:
// those are author-owned collections and a file that lists them is authoritative.
func (c *Config) applyDefaults() {
	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	if c.Listen.Anthropic == "" {
		c.Listen.Anthropic = DefaultAnthropicAddr
	}
	if c.Listen.OpenAI == "" {
		c.Listen.OpenAI = c.Listen.Anthropic
	}
	if c.Listen.Admin == "" {
		c.Listen.Admin = DefaultAdminAddr
	}
	if c.Budget.OnBreach == "" {
		c.Budget.OnBreach = "downgrade"
	}
	if c.Fallback.TTFTTimeout == 0 {
		c.Fallback.TTFTTimeout = 30 * time.Second
	}
	if c.Telemetry.RetentionDays == 0 {
		c.Telemetry.RetentionDays = 90
	}
	if c.Telemetry.Dir == "" {
		c.Telemetry.Dir = filepath.Join(Dir(), "telemetry")
	}
	for name, r := range c.Roles {
		if r.Tier == "" {
			r.Tier = "cheap"
			c.Roles[name] = r
		}
	}
}

// ProviderByID returns the provider with the given id.
func (c *Config) ProviderByID(id string) (Provider, bool) {
	for _, p := range c.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// ModelByID returns the registry entry with the given namespaced id.
func (c *Config) ModelByID(id string) (Model, bool) {
	for _, m := range c.Models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// RoleNames returns the configured role names, sorted.
func (c *Config) RoleNames() []string {
	names := make([]string, 0, len(c.Roles))
	for name := range c.Roles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ProviderIDs returns configured provider ids, sorted.
func (c *Config) ProviderIDs() []string {
	ids := make([]string, 0, len(c.Providers))
	for _, p := range c.Providers {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return ids
}

// TelemetryDir returns the configured telemetry directory or a default.
func (c *Config) TelemetryDir() string {
	if c.Telemetry.Dir != "" {
		return c.Telemetry.Dir
	}
	return filepath.Join(Dir(), "telemetry")
}

// LogDir returns the directory for gateway logs. It lives beside the active
// config file so a custom --config keeps its logs (and telemetry) together.
func (c *Config) LogDir() string {
	if c.path != "" {
		return filepath.Join(filepath.Dir(c.path), "logs")
	}
	return filepath.Join(Dir(), "logs")
}

// PIDFile returns the gateway pidfile path.
func (c *Config) PIDFile() string { return filepath.Join(Dir(), "gateway.pid") }

// String renders a short human summary.
func (c *Config) String() string {
	return fmt.Sprintf("vector config v%d: %d providers, %d models, %d roles, %d model_map rules",
		c.Version, len(c.Providers), len(c.Models), len(c.Roles), len(c.ModelMap))
}
