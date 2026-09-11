// Package router turns an inbound request into a concrete routing decision:
// which provider, which base URL, which upstream model, and whether a shape
// translation is required. It contains no network code, so it is fully
// unit-testable.
package router

import (
	"fmt"
	"strings"

	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/llm"
	"github.com/anunay999/vector/internal/registry"
)

// Virtual model prefixes. A request for "vector-worker", "vector/worker" or a
// bare configured role name resolves through the role table.
const (
	VirtualPrefixDash  = "vector-"
	VirtualPrefixSlash = "vector/"
)

// Traffic classes.
const (
	TrafficPrimary  = "primary"
	TrafficSubagent = "subagent"
)

// Default role used when a subagent is positively identified but provides no role.
const RoleSubagent = "worker"

// Known native model families. Requests for these are treated as primary
// traffic and passed through untouched unless a model_map rule overrides.
var nativePrefixes = []string{"claude", "opus", "sonnet", "haiku", "fable", "gpt-", "o1", "o3", "o4"}

// Input is the routing-relevant projection of a request.
type Input struct {
	Harness    string
	Shape      llm.Shape
	Model      string
	Headers    map[string]string
	IsSubagent bool
}

// Decision is a resolved route.
type Decision struct {
	Role          string
	Provider      config.Provider
	BaseURL       string
	UpstreamShape llm.Shape
	UpstreamModel string
	InboundShape  llm.Shape
	Translate     bool
	IsSubagent    bool
	Harness       string
	Reason        string
	Price         config.Price
}

// Router resolves requests against config + registry.
type Router struct {
	cfg *config.Config
	reg *registry.Registry
}

// New constructs a Router.
func New(cfg *config.Config, reg *registry.Registry) *Router {
	return &Router{cfg: cfg, reg: reg}
}

// IsVirtual reports whether a model string names a virtual (role) model.
func IsVirtual(model string) bool {
	return strings.HasPrefix(model, VirtualPrefixSlash) || strings.HasPrefix(model, VirtualPrefixDash)
}

// Route resolves input to a Decision.
func (r *Router) Route(in Input) (Decision, error) {
	if !r.cfg.RoutingEnabled {
		d, err := r.native(in, "routing disabled")
		return d, err
	}

	// 1. Explicit registry model id always wins and bypasses policy.
	if _, ok := r.reg.Lookup(in.Model); ok {
		return r.forModel(in, in.Model, "explicit model")
	}

	traffic, roleHint := r.classify(in)

	// 2. Agent mode, explicit: the request names a vector-<role> (or sends the
	// role header). The parent chose the agent, so resolve its preference list.
	if roleHint != "" {
		d, err := r.forRole(in, roleHint, "role "+roleHint, traffic == TrafficSubagent)
		if err == nil {
			return d, nil
		}
	}

	// 3. Model mode, explicit: model_map forces a concrete inbound model to a
	// target. This is how main-session Claude/Codex models get routed.
	if d, ok := r.modelMap(in, traffic); ok {
		return d, nil
	}

	// 4. Agent mode, automatic: a structurally detected subagent goes to the
	// worker agent when subagents.route is on (the default).
	if traffic == TrafficSubagent && r.cfg.Subagents.Routes() {
		d, err := r.forRole(in, RoleSubagent, "subagent -> "+RoleSubagent, true)
		if err == nil {
			return d, nil
		}
	}

	// 5. Everything else is subscription passthrough, unchanged.
	return r.native(in, "primary passthrough")
}

// NativePassthrough forwards a request to the native provider for its shape,
// preserving the requested model when it is a real model id. It is used for
// side-channel endpoints such as /v1/messages/count_tokens.
func (r *Router) NativePassthrough(in Input) (Decision, error) {
	return r.native(in, "native passthrough")
}

// classify returns the traffic class (primary|subagent) and an optional role
// hint derived from the requested virtual model or the role header.
func (r *Router) classify(in Input) (string, string) {
	if role, ok := r.virtualRole(in.Model); ok {
		return trafficFor(r.cfg.Roles[role]), role
	}
	if role := r.headerRole(in.Headers); role != "" {
		return trafficFor(r.cfg.Roles[role]), role
	}
	if in.IsSubagent {
		return TrafficSubagent, ""
	}
	return TrafficPrimary, ""
}

func trafficFor(role config.Role) string {
	if role.Primary {
		return TrafficPrimary
	}
	return TrafficSubagent
}

func (r *Router) virtualRole(model string) (string, bool) {
	name := model
	switch {
	case strings.HasPrefix(model, VirtualPrefixSlash):
		name = strings.TrimPrefix(model, VirtualPrefixSlash)
	case strings.HasPrefix(model, VirtualPrefixDash):
		name = strings.TrimPrefix(model, VirtualPrefixDash)
	default:
		return "", false
	}
	name = strings.TrimSpace(name)
	if _, ok := r.cfg.Roles[name]; ok {
		return name, true
	}
	return "", false
}

func (r *Router) headerRole(headers map[string]string) string {
	if headers == nil {
		return ""
	}
	for k, v := range headers {
		if strings.EqualFold(k, "X-Vector-Role") {
			v = strings.TrimSpace(v)
			if _, ok := r.cfg.Roles[v]; ok {
				return v
			}
		}
	}
	return ""
}

// modelMap applies the ordered model_map redirect table. It returns a decision
// when an inbound model matches a rule's From glob and the target resolves.
func (r *Router) modelMap(in Input, traffic string) (Decision, bool) {
	for _, rule := range r.cfg.ModelMap {
		if !matchGlob(rule.From, in.Model) {
			continue
		}
		if d, err := r.routeTarget(in, rule.To, traffic, "model_map "+rule.From); err == nil {
			return d, true
		}
	}
	return Decision{}, false
}

// matchGlob supports an exact match or a trailing '*'.
func matchGlob(pattern, s string) bool {
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(s, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == s
}

// routeTarget resolves a policy route (role name, registry model, or provider).
func (r *Router) routeTarget(in Input, target, traffic, reason string) (Decision, error) {
	subagent := traffic == TrafficSubagent
	if _, isRole := r.cfg.Roles[target]; isRole {
		return r.forRole(in, target, reason+" -> "+target, subagent)
	}
	if _, isModel := r.reg.Lookup(target); isModel {
		return r.resolveModel(in, target, reason+" -> "+target)
	}
	if p, isProvider := r.cfg.ProviderByID(target); isProvider {
		return r.forProvider(in, p, reason, subagent)
	}
	return Decision{}, fmt.Errorf("router: policy route %q is not a role, model, or provider", target)
}

func (r *Router) native(in Input, reason string) (Decision, error) {
	p, base, ok := r.nativeProviderFor(in.Shape)
	if !ok {
		return Decision{}, fmt.Errorf("router: no native provider for shape %s", in.Shape)
	}
	model, ok := r.nativeModel(in, p)
	if !ok {
		return Decision{}, fmt.Errorf("router: native provider %q has no model for virtual request %q (set default_model)", p.ID, in.Model)
	}
	d := Decision{
		Provider:      p,
		BaseURL:       base,
		UpstreamShape: in.Shape,
		UpstreamModel: model,
		InboundShape:  in.Shape,
		IsSubagent:    false,
		Harness:       in.Harness,
		Reason:        reason,
	}
	return d, nil
}

// nativeModel decides which concrete model to send to a native provider. A real
// inbound model is passed through; a virtual or namespaced model falls back to
// the provider's configured default_model.
func (r *Router) nativeModel(in Input, p config.Provider) (string, bool) {
	m := in.Model
	if m != "" && !IsVirtual(m) && !strings.Contains(m, "/") && !strings.HasPrefix(m, "vector") {
		return m, true
	}
	if p.DefaultModel != "" {
		return p.DefaultModel, true
	}
	return "", false
}

// forRole resolves a role through its preference list.
func (r *Router) forRole(in Input, role, reason string, subagent bool) (Decision, error) {
	return r.resolveRole(in, role, reason, subagent, map[string]bool{})
}

func (r *Router) resolveRole(in Input, role, reason string, subagent bool, seen map[string]bool) (Decision, error) {
	if seen[role] {
		return Decision{}, fmt.Errorf("router: role cycle at %q", role)
	}
	seen[role] = true

	def, ok := r.cfg.Roles[role]
	if !ok {
		return Decision{}, fmt.Errorf("router: unknown role %q", role)
	}
	for _, target := range def.Prefer {
		if _, isRole := r.cfg.Roles[target]; isRole {
			d, err := r.resolveRole(in, target, reason+" -> "+target, subagent, seen)
			if err == nil {
				d.Role = role
				return d, nil
			}
			continue
		}
		if e, isModel := r.reg.Lookup(target); isModel {
			p, ok := r.cfg.ProviderByID(e.ProviderID)
			if !ok {
				continue
			}
			if d, ok := r.build(in, role, p, e.Upstream, reason, subagent, e.Price); ok {
				return d, nil
			}
			continue
		}
		if p, isProvider := r.cfg.ProviderByID(target); isProvider {
			if d, err := r.forProvider(in, p, reason+" -> "+target, subagent); err == nil {
				d.Role = role
				return d, nil
			}
		}
	}
	return Decision{}, fmt.Errorf("router: role %q has no usable target for shape %s", role, in.Shape)
}

// forProvider resolves a concrete provider target.
func (r *Router) forProvider(in Input, p config.Provider, reason string, subagent bool) (Decision, error) {
	if !p.Native {
		if e, ok := r.reg.EntryForProvider(p.ID); ok {
			if d, ok := r.build(in, "", p, e.Upstream, reason, subagent, e.Price); ok {
				return d, nil
			}
		}
		return Decision{}, fmt.Errorf("router: provider %q has no usable model for shape %s", p.ID, in.Shape)
	}
	base, ok := providerBaseForShape(p, in.Shape)
	if !ok {
		return Decision{}, fmt.Errorf("router: native provider %q cannot serve shape %s", p.ID, in.Shape)
	}
	model, ok := r.nativeModel(in, p)
	if !ok {
		return Decision{}, fmt.Errorf("router: native provider %q has no default model", p.ID)
	}
	return Decision{
		Provider:      p,
		BaseURL:       base,
		UpstreamShape: in.Shape,
		UpstreamModel: model,
		InboundShape:  in.Shape,
		IsSubagent:    subagent,
		Harness:       in.Harness,
		Reason:        reason,
	}, nil
}

// build constructs a decision for a concrete provider+model, choosing the
// upstream shape that avoids translation when possible.
func (r *Router) build(in Input, role string, p config.Provider, upstream, reason string, subagent bool, price config.Price) (Decision, bool) {
	if base, ok := providerBaseForShape(p, in.Shape); ok {
		return Decision{
			Role: role, Provider: p, BaseURL: base,
			UpstreamShape: in.Shape, UpstreamModel: upstream, InboundShape: in.Shape,
			IsSubagent: subagent, Harness: in.Harness, Reason: reason, Price: price,
		}, true
	}
	nativeShape := providerNativeShape(p)
	if base, ok := providerBaseForShape(p, nativeShape); ok {
		return Decision{
			Role: role, Provider: p, BaseURL: base,
			UpstreamShape: nativeShape, UpstreamModel: upstream, InboundShape: in.Shape,
			Translate: in.Shape != nativeShape, IsSubagent: subagent, Harness: in.Harness,
			Reason: reason, Price: price,
		}, true
	}
	return Decision{}, false
}

func (r *Router) forModel(in Input, id, reason string) (Decision, error) {
	return r.resolveModel(in, id, reason)
}

// resolveModel builds a decision for a registry model.
func (r *Router) resolveModel(in Input, id, reason string) (Decision, error) {
	e, _ := r.reg.Lookup(id)
	p, ok := r.cfg.ProviderByID(e.ProviderID)
	if !ok {
		return Decision{}, fmt.Errorf("router: model %q references unknown provider %q", id, e.ProviderID)
	}
	d, ok := r.build(in, "", p, e.Upstream, reason, in.IsSubagent, e.Price)
	if !ok {
		return Decision{}, fmt.Errorf("router: provider %q cannot serve shape %s", p.ID, in.Shape)
	}
	return d, nil
}

func (r *Router) nativeProviderFor(shape llm.Shape) (config.Provider, string, bool) {
	for _, p := range r.cfg.Providers {
		if !p.Native {
			continue
		}
		if base, ok := providerBaseForShape(p, shape); ok {
			return p, base, true
		}
	}
	for _, p := range r.cfg.Providers {
		if base, ok := providerBaseForShape(p, shape); ok {
			return p, base, true
		}
	}
	return config.Provider{}, "", false
}

func providerNativeShape(p config.Provider) llm.Shape {
	switch p.Type {
	case config.ProviderAnthropic:
		return llm.ShapeAnthropic
	case config.ProviderOpenAIResponses:
		return llm.ShapeOpenAIResponses
	default:
		return llm.ShapeOpenAIChat
	}
}

// providerBaseForShape returns the base URL to use for the given shape and
// whether the provider can serve it natively (without translation).
func providerBaseForShape(p config.Provider, shape llm.Shape) (string, bool) {
	switch shape {
	case llm.ShapeAnthropic:
		if p.Type == config.ProviderAnthropic && p.BaseURL != "" {
			return p.BaseURL, true
		}
		if p.AnthropicBaseURL != "" {
			return p.AnthropicBaseURL, true
		}
		return "", false
	case llm.ShapeOpenAIChat:
		if p.Type == config.ProviderOpenAICompatible && p.BaseURL != "" {
			return p.BaseURL, true
		}
		return "", false
	case llm.ShapeOpenAIResponses:
		if p.Type == config.ProviderOpenAIResponses && p.BaseURL != "" {
			return p.BaseURL, true
		}
		if p.Type == config.ProviderOpenAICompatible && p.BaseURL != "" {
			return p.BaseURL, true
		}
		return "", false
	}
	return "", false
}

// NativePrefix reports whether a model id looks like a native frontier model.
func NativePrefix(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, p := range nativePrefixes {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}
