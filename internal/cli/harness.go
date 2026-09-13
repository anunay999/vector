package cli

import (
	"fmt"
	"os"
	"os/exec"

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
	cmd := harnessCommand("claude", "Wire Claude Code", func(c *config.Config) harness.Adapter {
		return harness.NewClaude(c)
	})
	cmd.AddCommand(
		newClaudeNativeCmd(),
		newHarnessAgentsCmd("List Claude Code subagents and their models", claudeManager),
		newHarnessRouteCmd("claude-code", "Route a Claude Code subagent to a role or model", claudeManager),
	)
	return cmd
}

// newClaudeNativeCmd runs Claude Code with the gateway env cleared for this one
// session. Claude Code disables Remote Control (and desktop remote sessions)
// whenever ANTHROPIC_BASE_URL points at a non-Anthropic host, so a session that
// needs those has to run natively. A --settings override outranks the user
// settings vector wrote, and an empty value is treated as unset.
func newClaudeNativeCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "native [claude args...]",
		Short:              "Run Claude Code without the gateway (needed for Remote Control)",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			override := `{"env":{"ANTHROPIC_BASE_URL":"","ENABLE_TOOL_SEARCH":"","CLAUDE_CODE_SUBAGENT_MODEL":"","CLAUDE_CODE_AUTO_COMPACT_WINDOW":""}}`
			argv := append([]string{"--settings", override}, args...)
			c := exec.Command("claude", argv...)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			return c.Run()
		},
	}
}

func newCodexCmd() *cobra.Command {
	cmd := harnessCommand("codex", "Wire Codex CLI", func(c *config.Config) harness.Adapter {
		return harness.NewCodex(c)
	})
	cmd.AddCommand(
		newHarnessAgentsCmd("List Codex agent roles and their models", codexManager),
		newHarnessRouteCmd("codex", "Route a Codex agent role to a role or model", codexManager),
	)
	return cmd
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
