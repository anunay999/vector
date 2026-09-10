package cli

import (
	"fmt"

	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/harness"
	"github.com/spf13/cobra"
)

// adapters returns the harness adapters for a config.
func adapters(cfg *config.Config) []harness.Adapter {
	return []harness.Adapter{
		harness.NewClaude(cfg),
		harness.NewCodex(cfg),
		harness.NewOpenCode(cfg),
	}
}

func harnessCommand(name, short string, makeAdapter func(*config.Config) harness.Adapter) *cobra.Command {
	cmd := &cobra.Command{Use: name, Short: short}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "on",
			Short: "Install vector wiring for " + name,
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := loadConfig()
				if err != nil {
					return err
				}
				rep, err := makeAdapter(cfg).Enable()
				if err != nil {
					return err
				}
				fmt.Printf("%s: %s\n", name, onOff(rep.Changed))
				for _, a := range rep.Actions {
					fmt.Printf("  - %s: %s\n", a.Target, a.Detail)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "off",
			Short: "Remove vector wiring for " + name,
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := loadConfig()
				if err != nil {
					return err
				}
				rep, err := makeAdapter(cfg).Disable()
				if err != nil {
					return err
				}
				fmt.Printf("%s: %s\n", name, onOff(rep.Changed))
				for _, a := range rep.Actions {
					fmt.Printf("  - %s: %s\n", a.Target, a.Detail)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show vector wiring for " + name,
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := loadConfig()
				if err != nil {
					return err
				}
				st, err := makeAdapter(cfg).Status()
				if err != nil {
					return err
				}
				fmt.Printf("%s: %s (%s)\n", name, onOff(st.Enabled), st.Detail)
				return nil
			},
		},
	)
	return cmd
}

func newClaudeCmd() *cobra.Command {
	return harnessCommand("claude", "Wire Claude Code", func(c *config.Config) harness.Adapter {
		return harness.NewClaude(c)
	})
}

func newCodexCmd() *cobra.Command {
	return harnessCommand("codex", "Wire Codex CLI", func(c *config.Config) harness.Adapter {
		return harness.NewCodex(c)
	})
}

func newOpenCodeCmd() *cobra.Command {
	return harnessCommand("opencode", "Wire OpenCode", func(c *config.Config) harness.Adapter {
		return harness.NewOpenCode(c)
	})
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "no changes"
}
