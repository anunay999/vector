package cli

import (
	"fmt"

	"github.com/anunay999/vector/internal/service"
	"github.com/spf13/cobra"
)

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Install vector as a user background service",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "install",
			Short: "Install and start the service (launchd/systemd)",
			RunE: func(cmd *cobra.Command, args []string) error {
				st, err := service.Install(configPath())
				if err != nil {
					return err
				}
				fmt.Printf("installed %s service at %s (%s)\n", st.Platform, st.Path, st.Detail)
				return nil
			},
		},
		&cobra.Command{
			Use:   "uninstall",
			Short: "Stop and remove the service",
			RunE: func(cmd *cobra.Command, args []string) error {
				st, err := service.Uninstall()
				if err != nil {
					return err
				}
				fmt.Printf("removed %s service (%s)\n", st.Platform, st.Detail)
				return nil
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show service status",
			Run: func(cmd *cobra.Command, args []string) {
				st := service.Current()
				state := "not installed"
				if st.Installed {
					state = "installed"
					if st.Active {
						state = "active"
					} else {
						state = "installed (inactive)"
					}
				}
				fmt.Printf("%s: %s\n", st.Platform, state)
				if st.Path != "" {
					fmt.Printf("  path: %s\n", st.Path)
				}
			},
		},
	)
	return cmd
}
