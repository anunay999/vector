package cli

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"

	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/version"
	"github.com/spf13/cobra"
)

//go:embed agent_guide.md
var agentGuide string

type harnessState struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Detail  string `json:"detail"`
}

type guideState struct {
	Version        string         `json:"version"`
	ConfigPath     string         `json:"config_path"`
	EnvPath        string         `json:"env_path"`
	ConfigExists   bool           `json:"config_exists"`
	RoutingEnabled bool           `json:"routing_enabled"`
	GatewayRunning bool           `json:"gateway_running"`
	UnresolvedVars []string       `json:"unresolved_vars"`
	Providers      []string       `json:"providers"`
	Roles          []string       `json:"roles"`
	Harnesses      []harnessState `json:"harnesses"`
	NextActions    []string       `json:"next_actions"`
}

func newGuideCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "guide",
		Aliases: []string{"agent"},
		Short:   "Print the agent setup guide and live state",
		Long: "Prints a self-contained configuration guide plus the current machine\n" +
			"state (config, keys, gateway, harness wiring) so an AI agent can set vector\n" +
			"up without human help. Use --json for a machine-readable form.",
		RunE: func(cmd *cobra.Command, args []string) error {
			st := collectGuideState()
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"state": st, "guide": agentGuide})
			}
			fmt.Print(agentGuide)
			fmt.Print("\n---\n\n## Current state\n\n")
			fmt.Printf("- version: %s\n", st.Version)
			fmt.Printf("- config: %s (exists=%v)\n", st.ConfigPath, st.ConfigExists)
			fmt.Printf("- env: %s\n", st.EnvPath)
			fmt.Printf("- routing enabled: %v\n", st.RoutingEnabled)
			fmt.Printf("- gateway running: %v\n", st.GatewayRunning)
			if len(st.UnresolvedVars) > 0 {
				fmt.Printf("- unresolved vars: %v\n", st.UnresolvedVars)
			}
			if len(st.Harnesses) > 0 {
				fmt.Println("- harnesses:")
				for _, h := range st.Harnesses {
					state := "off"
					if h.Enabled {
						state = "on"
					}
					fmt.Printf("    - %s: %s (%s)\n", h.Name, state, h.Detail)
				}
			}
			if len(st.NextActions) > 0 {
				fmt.Println("\nNext actions:")
				for _, a := range st.NextActions {
					fmt.Printf("  - %s\n", a)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON including the guide text")
	return cmd
}

func collectGuideState() guideState {
	st := guideState{
		Version:    version.String(),
		ConfigPath: configPath(),
		EnvPath:    config.EnvPath(),
	}
	cfg, err := loadConfig()
	if err == nil {
		st.RoutingEnabled = cfg.RoutingEnabled
		st.Providers = cfg.ProviderIDs()
		st.Roles = cfg.RoleNames()
		st.UnresolvedVars = cfg.UnresolvedVars()
		st.GatewayRunning = probeHealth(cfg)
		for _, a := range adapters(cfg) {
			hs := harnessState{Name: a.Name()}
			if s, serr := a.Status(); serr == nil {
				hs.Enabled = s.Enabled
				hs.Detail = s.Detail
			}
			st.Harnesses = append(st.Harnesses, hs)
		}
	} else {
		st.NextActions = append(st.NextActions, "config is invalid: "+err.Error())
	}

	if _, err := os.Stat(st.ConfigPath); err != nil {
		st.NextActions = append(st.NextActions, "vector config init")
	} else {
		st.ConfigExists = true
	}
	if len(st.UnresolvedVars) > 0 {
		st.NextActions = append(st.NextActions, "vector env set <VAR> <value>")
	}
	if cfg != nil && !st.GatewayRunning {
		st.NextActions = append(st.NextActions, "vector up")
	}
	for _, h := range st.Harnesses {
		if !h.Enabled {
			st.NextActions = append(st.NextActions, "vector "+shortName(h.Name)+" on")
		}
	}
	return st
}
