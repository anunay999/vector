package config

import (
	"regexp"
	"sort"
	"strings"
)

// BuiltinReferenceAsOf records when the built-in list prices were captured.
// The table mirrors the providers' published API list prices (as relayed by
// OpenRouter's public model catalog on that date). Prices move; override any
// entry with `reference_prices` in config.yaml, and check the table with
// `vector models reference`.
const BuiltinReferenceAsOf = "2026-09-11"

// builtinReference is the API list price, USD per million tokens, of the models
// a Claude Code or Codex subscription session asks for. It is only ever used to
// price the tokens routing kept off the subscription, so a saving can be
// estimated; nothing here is billed. Keys are canonical: lowercase, dots as
// dashes, no provider prefix, no date suffix, no [1m] alias.
var builtinReference = map[string]Price{
	// Anthropic
	"claude-fable-5-1":  {In: 10, Out: 50, CacheRead: 0.25, CacheWrite: 12.5},
	"claude-fable-5":    {In: 10, Out: 50, CacheRead: 1, CacheWrite: 12.5},
	"claude-opus-5":     {In: 5, Out: 25, CacheRead: 0.5, CacheWrite: 6.25},
	"claude-opus-4-8":   {In: 5, Out: 25, CacheRead: 0.5, CacheWrite: 6.25},
	"claude-opus-4-7":   {In: 5, Out: 25, CacheRead: 0.5, CacheWrite: 6.25},
	"claude-opus-4-6":   {In: 5, Out: 25, CacheRead: 0.5, CacheWrite: 6.25},
	"claude-opus-4-5":   {In: 5, Out: 25, CacheRead: 0.5, CacheWrite: 6.25},
	"claude-opus-4-1":   {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75},
	"claude-opus-4":     {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75},
	"claude-sonnet-5":   {In: 2, Out: 10, CacheRead: 0.2, CacheWrite: 2.5},
	"claude-sonnet-4-6": {In: 3, Out: 15, CacheRead: 0.3, CacheWrite: 3.75},
	"claude-sonnet-4-5": {In: 3, Out: 15, CacheRead: 0.3, CacheWrite: 3.75},
	"claude-sonnet-4":   {In: 3, Out: 15, CacheRead: 0.3, CacheWrite: 3.75},
	"claude-haiku-4-5":  {In: 1, Out: 5, CacheRead: 0.1, CacheWrite: 1.25},
	"claude-3-haiku":    {In: 0.25, Out: 1.25, CacheRead: 0.03, CacheWrite: 0.3},

	// OpenAI (Codex). OpenAI publishes no separate cache-write price.
	"gpt-6-astra":        {In: 10, Out: 50, CacheRead: 1, CacheWrite: 12.5},
	"gpt-6-astra-pro":    {In: 10, Out: 50, CacheRead: 1, CacheWrite: 12.5},
	"gpt-5-6-terra":      {In: 2, Out: 12, CacheRead: 0.2, CacheWrite: 2.5},
	"gpt-5-6-terra-pro":  {In: 2, Out: 12, CacheRead: 0.2, CacheWrite: 2.5},
	"gpt-5-6-sol":        {In: 2, Out: 10, CacheRead: 0.2, CacheWrite: 2.5},
	"gpt-5-6-sol-pro":    {In: 2, Out: 10, CacheRead: 0.2, CacheWrite: 2.5},
	"gpt-5-6-luna":       {In: 0.2, Out: 1.2, CacheRead: 0.02, CacheWrite: 0.25},
	"gpt-5-6-luna-pro":   {In: 0.2, Out: 1.2, CacheRead: 0.02, CacheWrite: 0.25},
	"gpt-5-5":            {In: 5, Out: 30, CacheRead: 0.5},
	"gpt-5-5-pro":        {In: 30, Out: 180},
	"gpt-5-4":            {In: 2.5, Out: 15, CacheRead: 0.25},
	"gpt-5-4-mini":       {In: 0.75, Out: 4.5, CacheRead: 0.075},
	"gpt-5-4-nano":       {In: 0.2, Out: 1.25, CacheRead: 0.02},
	"gpt-5-4-pro":        {In: 30, Out: 180},
	"gpt-5-3-codex":      {In: 1.75, Out: 14, CacheRead: 0.175},
	"gpt-5-2":            {In: 1.75, Out: 14, CacheRead: 0.175},
	"gpt-5-2-codex":      {In: 1.75, Out: 14, CacheRead: 0.175},
	"gpt-5-2-pro":        {In: 21, Out: 168},
	"gpt-5-1":            {In: 1.25, Out: 10, CacheRead: 0.125},
	"gpt-5-1-codex":      {In: 1.25, Out: 10, CacheRead: 0.13},
	"gpt-5-1-codex-max":  {In: 1.25, Out: 10, CacheRead: 0.125},
	"gpt-5-1-codex-mini": {In: 0.25, Out: 2, CacheRead: 0.03},
	"gpt-5":              {In: 1.25, Out: 10, CacheRead: 0.125},
	"gpt-5-mini":         {In: 0.25, Out: 2, CacheRead: 0.025},
	"gpt-5-nano":         {In: 0.05, Out: 0.4, CacheRead: 0.005},
	"gpt-5-pro":          {In: 15, Out: 120},
	"gpt-4-1":            {In: 2, Out: 8, CacheRead: 0.5},
	"gpt-4-1-mini":       {In: 0.4, Out: 1.6, CacheRead: 0.1},
	"gpt-4-1-nano":       {In: 0.1, Out: 0.4, CacheRead: 0.025},
	"gpt-4o":             {In: 2.5, Out: 10, CacheRead: 1.25},
	"gpt-4o-mini":        {In: 0.15, Out: 0.6, CacheRead: 0.075},
	"o3":                 {In: 2, Out: 8, CacheRead: 0.5},
	"o3-pro":             {In: 20, Out: 80},
	"o4-mini":            {In: 1.1, Out: 4.4, CacheRead: 0.275},
}

// Harness aliases that reach the gateway without a concrete id. Claude Code
// normally sends the real id, but a settings `model` alias can leak through
// count_tokens and a Codex profile may name a family.
var referenceAliases = map[string]string{
	"opus":     "claude-opus-5",
	"opusplan": "claude-opus-5",
	"sonnet":   "claude-sonnet-5",
	"haiku":    "claude-haiku-4-5",
	"fable":    "claude-fable-5-1",
}

var (
	dateSuffix   = regexp.MustCompile(`-(\d{8}|\d{4}-\d{2}-\d{2}|latest)$`)
	oneMSuffix   = regexp.MustCompile(`\[1m\]$`)
	providerPref = regexp.MustCompile(`^[a-z0-9_.-]+/`)
)

// CanonicalModel reduces a wire model id to the built-in table's key form:
// "anthropic/claude-opus-4.6[1m]" → "claude-opus-4-6",
// "claude-sonnet-4-5-20250929" → "claude-sonnet-4-5", "gpt-5.4-mini" →
// "gpt-5-4-mini". Aliases resolve to their current default.
func CanonicalModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	m = oneMSuffix.ReplaceAllString(m, "")
	m = providerPref.ReplaceAllString(m, "")
	m = strings.TrimSuffix(m, ":batch")
	m = dateSuffix.ReplaceAllString(m, "")
	m = strings.ReplaceAll(m, ".", "-")
	if a, ok := referenceAliases[m]; ok {
		return a
	}
	return m
}

// BuiltinReference looks a model up in the built-in table.
func BuiltinReference(model string) (Price, bool) {
	p, ok := builtinReference[CanonicalModel(model)]
	return p, ok
}

// ReferencePriceFor resolves the list price a model would have been billed at:
// a `reference_prices` override first, then the built-in table.
func (c *Config) ReferencePriceFor(model string) (Price, bool) {
	key := CanonicalModel(model)
	for k, p := range c.ReferencePrices {
		if CanonicalModel(k) == key {
			return p, true
		}
	}
	return BuiltinReference(model)
}

// ProviderReferencePrice resolves the price a native provider's traffic is
// measured against when the requested model is not itself priceable (a virtual
// role such as vector-worker): an explicit reference_price, else the list
// price of its default_model.
func (c *Config) ProviderReferencePrice(p Provider) (Price, bool) {
	if p.ReferencePrice != nil {
		return *p.ReferencePrice, true
	}
	if p.DefaultModel == "" {
		return Price{}, false
	}
	return c.ReferencePriceFor(p.DefaultModel)
}

// ReferenceEntry is one row of the effective reference table.
type ReferenceEntry struct {
	Model  string `json:"model"`
	Price  Price  `json:"price"`
	Source string `json:"source"` // builtin | override
}

// ReferenceTable returns the effective reference table, sorted by model,
// with config overrides applied over the built-in entries.
func (c *Config) ReferenceTable() []ReferenceEntry {
	rows := map[string]ReferenceEntry{}
	for k, p := range builtinReference {
		rows[k] = ReferenceEntry{Model: k, Price: p, Source: "builtin"}
	}
	for k, p := range c.ReferencePrices {
		key := CanonicalModel(k)
		rows[key] = ReferenceEntry{Model: key, Price: p, Source: "override"}
	}
	out := make([]ReferenceEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}
