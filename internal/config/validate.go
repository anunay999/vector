package config

import (
	"fmt"
	"strings"
)

// Validate checks structural integrity. It returns the first problem found. It
// is read-only: defaults are applied by applyDefaults during load, not here.
func (c *Config) Validate() error {
	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	if c.Version != CurrentVersion {
		return fmt.Errorf("unsupported config version %d (want %d)", c.Version, CurrentVersion)
	}
	if c.Listen.Anthropic == "" || c.Listen.OpenAI == "" {
		return fmt.Errorf("listen.anthropic and listen.openai are required")
	}

	if len(c.Providers) == 0 {
		return fmt.Errorf("at least one provider is required")
	}
	providers := map[string]bool{}
	for i := range c.Providers {
		p := &c.Providers[i]
		if p.ID == "" {
			return fmt.Errorf("providers[%d]: id is required", i)
		}
		if providers[p.ID] {
			return fmt.Errorf("providers[%d]: duplicate id %q", i, p.ID)
		}
		providers[p.ID] = true
		switch p.Type {
		case ProviderOpenAICompatible, ProviderAnthropic, ProviderOpenAIResponses:
		case "":
			return fmt.Errorf("provider %q: type is required", p.ID)
		default:
			return fmt.Errorf("provider %q: unknown type %q", p.ID, p.Type)
		}
		if strings.TrimSpace(p.BaseURL) == "" {
			return fmt.Errorf("provider %q: base_url is required", p.ID)
		}
	}

	models := map[string]bool{}
	for i, m := range c.Models {
		if m.ID == "" {
			return fmt.Errorf("models[%d]: id is required", i)
		}
		if models[m.ID] {
			return fmt.Errorf("models[%d]: duplicate id %q", i, m.ID)
		}
		models[m.ID] = true
		if !strings.Contains(m.ID, "/") {
			return fmt.Errorf("model %q: id must be namespaced as provider/model", m.ID)
		}
	}

	for name, r := range c.Roles {
		if len(r.Prefer) == 0 {
			return fmt.Errorf("role %q: at least one preferred target is required", name)
		}
		switch r.Tier {
		case "frontier", "smart", "cheap":
		default:
			return fmt.Errorf("role %q: unknown tier %q", name, r.Tier)
		}
		for _, target := range r.Prefer {
			if err := c.validateTarget(target, providers, models); err != nil {
				return fmt.Errorf("role %q: %w", name, err)
			}
		}
	}
	if err := c.checkRoleCycles(); err != nil {
		return err
	}

	for i, rule := range c.ModelMap {
		if strings.TrimSpace(rule.From) == "" {
			return fmt.Errorf("model_map[%d]: from is required", i)
		}
		if strings.TrimSpace(rule.To) == "" {
			return fmt.Errorf("model_map[%d]: to is required", i)
		}
		if err := c.validateTarget(rule.To, providers, models); err != nil {
			return fmt.Errorf("model_map[%d]: %w", i, err)
		}
	}

	switch c.Budget.OnBreach {
	case "", "downgrade", "queue", "stop":
	default:
		return fmt.Errorf("budget.on_breach: unknown value %q", c.Budget.OnBreach)
	}
	return nil
}

// checkRoleCycles rejects a role preference graph that contains a cycle, which
// would otherwise only surface at request time.
func (c *Config) checkRoleCycles() error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var visit func(role string, path []string) error
	visit = func(role string, path []string) error {
		switch color[role] {
		case gray:
			return fmt.Errorf("role cycle: %s -> %s", strings.Join(path, " -> "), role)
		case black:
			return nil
		}
		color[role] = gray
		for _, target := range c.Roles[role].Prefer {
			if _, isRole := c.Roles[target]; isRole {
				if err := visit(target, append(path, role)); err != nil {
					return err
				}
			}
		}
		color[role] = black
		return nil
	}
	for name := range c.Roles {
		if err := visit(name, nil); err != nil {
			return err
		}
	}
	return nil
}

// validateTarget accepts a role name, a namespaced model id, or a provider id.
func (c *Config) validateTarget(target string, providers, models map[string]bool) error {
	if target == "" {
		return fmt.Errorf("empty target")
	}
	if _, ok := c.Roles[target]; ok {
		return nil
	}
	if models[target] {
		return nil
	}
	if providers[target] {
		return nil
	}
	if pid, _, ok := strings.Cut(target, "/"); ok && providers[pid] {
		return nil
	}
	return fmt.Errorf("unresolved target %q (not a role, registered model, or known provider/model)", target)
}
