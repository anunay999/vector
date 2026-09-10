package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/harness"
	"github.com/spf13/cobra"
)

// newAgentsCmd lists every agent/role across the wired harnesses.
func newAgentsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "List subagents across harnesses and whether they are routed",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			var all []harness.Agent
			for _, m := range agentManagers(cfg) {
				list, err := m.Agents()
				if err != nil {
					continue
				}
				all = append(all, list...)
			}
			return printAgents(all, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}

func agentManagers(cfg *config.Config) []harness.AgentManager {
	return []harness.AgentManager{
		harness.NewClaude(cfg),
		harness.NewCodex(cfg),
	}
}

func printAgents(agents []harness.Agent, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(agents)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "HARNESS\tAGENT\tMODEL\tROUTED\tMANAGED")
	for _, a := range agents {
		model := a.Model
		if model == "" {
			model = "(inline)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%v\t%v\n", a.Harness, a.Name, model, a.Routed, a.Managed)
	}
	w.Flush()
	return nil
}

// newHarnessAgentsCmd lists agents for one harness.
func newHarnessAgentsCmd(short string, mgr func(*config.Config) harness.AgentManager) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "agents",
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			list, err := mgr(cfg).Agents()
			if err != nil {
				return err
			}
			return printAgents(list, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}

// newHarnessRouteCmd points an agent at a model. The target is optional: when
// omitted it is the role of the same name, so `route scout` routes the scout
// agent to the scout role.
func newHarnessRouteCmd(harnessName, short string, mgr func(*config.Config) harness.AgentManager) *cobra.Command {
	var all, dryRun bool
	var roleFlag, modelFlag string
	cmd := &cobra.Command{
		Use:   "route <agent> [role] | route --all [role]",
		Short: short,
		Long: short + "\n\nWith no target, an agent is routed to the role of the same\n" +
			"name. A target is a role (worker), a virtual model (vector-worker), or an\n" +
			"explicit provider/model id (openrouter/z-ai/glm-5.3-flash).",
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			m := mgr(cfg)

			if len(args) == 0 && !all {
				return fmt.Errorf("usage: route <agent> [role], or route --all [role]")
			}
			if all && len(args) == 0 && roleFlag == "" && modelFlag == "" {
				// route --all with no target: each agent to its same-named role.
			}

			// Resolve the explicit target, if any.
			explicit := ""
			switch {
			case modelFlag != "":
				explicit = modelFlag
			case roleFlag != "":
				explicit = roleFlag
			case len(args) == 2:
				explicit = args[1]
			}
			if roleFlag != "" {
				if _, ok := cfg.Roles[roleFlag]; !ok {
					return fmt.Errorf("role %q is not configured (roles: %s)", roleFlag, strings.Join(cfg.RoleNames(), ", "))
				}
			}

			var names []string
			if all {
				list, err := m.Agents()
				if err != nil {
					return err
				}
				for _, a := range list {
					if !a.Managed {
						names = append(names, a.Name)
					}
				}
				if len(names) == 0 {
					fmt.Println("no non-managed agents to route")
					return nil
				}
			} else {
				names = []string{args[0]}
			}

			for _, name := range names {
				target := explicit
				if target == "" {
					if _, ok := cfg.Roles[name]; !ok {
						msg := fmt.Sprintf("%q is not a configured role", name)
						if all {
							fmt.Printf("skip %s: %s (pass a target)\n", name, msg)
							continue
						}
						return fmt.Errorf("%s; pass a target or --model <provider/model>", msg)
					}
					target = name
				}
				model, provider, err := resolveAgentModel(cfg, harnessName, target)
				if err != nil {
					return err
				}
				if dryRun {
					fmt.Printf("would route %s -> %s\n", name, model)
					continue
				}
				if err := m.SetAgentModel(name, model, provider); err != nil {
					if all {
						fmt.Printf("skip %s: %v\n", name, err)
						continue
					}
					return err
				}
				fmt.Printf("routed %s -> %s\n", name, model)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "apply to every non-managed agent")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change")
	cmd.Flags().StringVar(&roleFlag, "role", "", "route to this role")
	cmd.Flags().StringVar(&modelFlag, "model", "", "route to this provider/model id")
	return cmd
}

// resolveAgentModel turns a target into a concrete model (and provider).
// A configured role or vector-* name becomes the harness virtual model;
// anything else is treated as an explicit model id (or native alias).
func resolveAgentModel(cfg *config.Config, harnessName, target string) (model, provider string, err error) {
	t := strings.TrimSpace(target)
	if t == "" {
		return "", "", fmt.Errorf("empty target")
	}
	if strings.HasPrefix(t, "vector") {
		return harness.VirtualModel(harnessName, t)
	}
	if _, ok := cfg.Roles[t]; ok {
		return harness.VirtualModel(harnessName, t)
	}
	if strings.Contains(t, "/") {
		if harnessName == "codex" {
			pid, _, _ := strings.Cut(t, "/")
			return t, pid, nil
		}
		return t, "", nil
	}
	// A bare word that is not a role: a native alias or raw model id.
	return t, "", nil
}

func claudeManager(c *config.Config) harness.AgentManager { return harness.NewClaude(c) }
func codexManager(c *config.Config) harness.AgentManager  { return harness.NewCodex(c) }
