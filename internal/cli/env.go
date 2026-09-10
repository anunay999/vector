package cli

import (
	"fmt"
	"sort"

	"github.com/anunay999/vector/internal/config"
	"github.com/spf13/cobra"
)

func newEnvCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "env", Short: "Manage the secrets env file"}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "path",
			Short: "Print the env file path",
			Run:   func(cmd *cobra.Command, args []string) { fmt.Println(config.EnvPath()) },
		},
		&cobra.Command{
			Use:   "list",
			Short: "List configured keys (values redacted)",
			RunE: func(cmd *cobra.Command, args []string) error {
				m, err := config.ReadEnv()
				if err != nil {
					return err
				}
				keys := make([]string, 0, len(m))
				for k := range m {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				if len(keys) == 0 {
					fmt.Printf("(empty) %s\n", config.EnvPath())
					return nil
				}
				for _, k := range keys {
					fmt.Printf("%s=%s\n", k, redact(m[k]))
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "set <KEY> <value>",
			Short: "Set a secret in the env file",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := config.SetEnv(args[0], args[1]); err != nil {
					return err
				}
				fmt.Printf("set %s in %s\n", args[0], config.EnvPath())
				return nil
			},
		},
		&cobra.Command{
			Use:   "unset <KEY>",
			Short: "Remove a secret from the env file",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if err := config.UnsetEnv(args[0]); err != nil {
					return err
				}
				fmt.Printf("unset %s\n", args[0])
				return nil
			},
		},
	)
	return cmd
}

func redact(v string) string {
	if len(v) <= 8 {
		return "********"
	}
	return v[:6] + "…" + v[len(v)-4:]
}
