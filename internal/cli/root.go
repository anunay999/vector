// Package cli implements the vector command-line interface.
package cli

import (
	"fmt"
	"os"

	"github.com/anunay999/vector/internal/config"
	"github.com/anunay999/vector/internal/version"
	"github.com/spf13/cobra"
)

var cfgPath string

// Execute runs the root command.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "vector:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "vector",
		Short: "Intelligent subagent model router for any harness",
		Long: "Vector routes the subagents your coding harness spawns to cost-efficient\n" +
			"models while leaving frontier planning on Claude and Codex.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String(),
	}
	root.PersistentFlags().StringVar(&cfgPath, "config", "", "config file (default ~/.config/vector/config.yaml)")

	root.AddCommand(
		newInitCmd(),
		newSetupCmd(),
		newGuideCmd(),
		newSchemaCmd(),
		newServeCmd(),
		newUpCmd(),
		newDownCmd(),
		newRestartCmd(),
		newStatusCmd(),
		newConfigCmd(),
		newEnvCmd(),
		newServiceCmd(),
		newClaudeCmd(),
		newCodexCmd(),
		newOpenCodeCmd(),
		newModelsCmd(),
		newSpendCmd(),
		newDoctorCmd(),
	)
	return root
}

func loadConfig() (*config.Config, error) {
	if cfgPath != "" {
		return config.LoadFrom(cfgPath)
	}
	return config.Load()
}
