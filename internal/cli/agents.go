package cli

import (
	"encoding/json"
	"fmt"
	"os"
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

// newHarnessRouteCmd points one agent (or all) at a virtual model.
func newHarnessRouteCmd(short string, mgr func(*config.Config) harness.AgentManager) *cobra.Command {
	var all, dryRun bool
	cmd := &cobra.Command{
		Use:   "route <agent> <target> | route --all <target>",
		Short: short,
		Long: short + "\n\nA target is a role name (worker), a virtual model\n" +
			"(vector-worker / vector/worker), or a provider/model id.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			m := mgr(cfg)
			var name, target string
			if len(args) == 2 {
				name, target = args[0], args[1]
			} else if all {
				target = args[0]
			} else {
				return fmt.Errorf("usage: route <agent> <target>, or route --all <target>")
			}

			names := []string{name}
			if all {
				names = nil
				list, err := m.Agents()
				if err != nil {
					return err
				}
				for _, a := range list {
					if !a.Managed {
						names = append(names, a.Name)
					}
				}
			}
			for _, n := range names {
				if dryRun {
					fmt.Printf("would route %s -> %s\n", n, target)
					continue
				}
				if err := m.SetAgentModel(n, target); err != nil {
					return err
				}
				fmt.Printf("routed %s -> %s\n", n, target)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "apply to every non-managed agent")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change")
	return cmd
}

func claudeManager(c *config.Config) harness.AgentManager { return harness.NewClaude(c) }
func codexManager(c *config.Config) harness.AgentManager  { return harness.NewCodex(c) }
