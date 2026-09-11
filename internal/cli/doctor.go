package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/service"
	"github.com/spf13/cobra"
)

type check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | fail
	Detail string `json:"detail"`
}

func newDoctorCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose configuration, gateway, and harness wiring",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			checks := runChecks(cfg)

			exit := 0
			for _, c := range checks {
				if c.Status == "fail" {
					exit = 1
				}
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				_ = enc.Encode(map[string]any{"checks": checks, "ok": exit == 0})
			} else {
				for _, c := range checks {
					icon := "✓"
					switch c.Status {
					case "warn":
						icon = "!"
					case "fail":
						icon = "✗"
					}
					fmt.Printf("%s %-22s %s\n", icon, c.Name, c.Detail)
				}
			}
			if exit != 0 {
				os.Exit(exit)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}

// runChecks performs the full read-only diagnosis shared by `doctor` and `setup`.
func runChecks(cfg *config.Config) []check {
	var checks []check
	add := func(name, status, detail string) {
		checks = append(checks, check{Name: name, Status: status, Detail: detail})
	}

	if err := cfg.Validate(); err != nil {
		add("config", "fail", err.Error())
	} else {
		add("config", "ok", cfg.Path())
	}

	if missing := cfg.UnresolvedVars(); len(missing) > 0 {
		add("env", "warn", fmt.Sprintf("unresolved: %v", missing))
	} else {
		add("env", "ok", "all referenced variables resolved")
	}

	for _, p := range cfg.Providers {
		switch {
		case p.Native:
			add("provider/"+p.ID, "ok", "native passthrough (inbound credential)")
		case p.APIKey == "":
			add("provider/"+p.ID, "warn", "no api_key configured")
		default:
			add("provider/"+p.ID, "ok", "key configured")
		}
	}

	if probeHealth(cfg) {
		add("gateway", "ok", cfg.Listen.Anthropic)
	} else {
		add("gateway", "warn", "not reachable; run 'vector up'")
	}

	svc := service.Current()
	if svc.Installed {
		state := "installed"
		if svc.Active {
			state = "active"
		}
		add("service", "ok", state+" ("+svc.Platform+")")
	} else {
		add("service", "warn", "not installed; run 'vector service install'")
	}

	for _, a := range adapters(cfg) {
		st, err := a.Status()
		if err != nil {
			add("harness/"+a.Name(), "fail", err.Error())
			continue
		}
		if st.Enabled {
			add("harness/"+a.Name(), "ok", st.Detail)
		} else {
			add("harness/"+a.Name(), "warn", "not wired; run 'vector "+shortName(a.Name())+" on'")
		}
		if a.Name() == "claude-code" && st.Enabled {
			checks = append(checks, claudeContextChecks(st.Info)...)
		}
	}
	return checks
}

// claudeContextChecks flags Claude Code settings that make a gateway session
// re-send its whole tool inventory on every request or lose its 1M window.
func claudeContextChecks(info map[string]string) []check {
	var out []check
	switch v := info["tool_search"]; {
	case v == "":
		out = append(out, check{Name: "claude/tool-search", Status: "warn",
			Detail: "ENABLE_TOOL_SEARCH unset: Claude Code disables deferred tool loading behind a gateway URL and re-sends every MCP tool schema on every request; run 'vector claude on' to set ENABLE_TOOL_SEARCH=auto"})
	case v == "false" || v == "0":
		out = append(out, check{Name: "claude/tool-search", Status: "warn", Detail: "ENABLE_TOOL_SEARCH=" + v + " (deferred tool loading off)"})
	default:
		out = append(out, check{Name: "claude/tool-search", Status: "ok", Detail: "ENABLE_TOOL_SEARCH=" + v})
	}
	if m := info["model"]; strings.Contains(strings.ToLower(m), "[1m]") {
		out = append(out, check{Name: "claude/1m-context", Status: "warn",
			Detail: fmt.Sprintf("model %q: Claude Code only treats api.anthropic.com as 1M-entitled; behind a gateway the session can drop to a 200k auto-compact window, which loops when tool schemas exceed ~150k tokens. Pin it with CLAUDE_CODE_AUTO_COMPACT_WINDOW or keep the tool inventory small", m)})
	}
	return out
}

func shortName(harness string) string {
	switch harness {
	case "claude-code":
		return "claude"
	case "codex":
		return "codex"
	case "opencode":
		return "opencode"
	}
	return harness
}
