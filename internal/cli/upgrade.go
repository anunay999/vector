package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/anunay999/vector/internal/service"
	"github.com/anunay999/vector/internal/version"
	"github.com/spf13/cobra"
)

const installScriptURL = "https://raw.githubusercontent.com/anunay999/vector/main/install.sh"

func newUpgradeCmd() *cobra.Command {
	var script string
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Update the vector binary and restart the service",
		Long: "Re-runs the official installer — a release download with checksum, or\n" +
			"`go install` when no release is published — then restarts the service if\n" +
			"one is installed. Use --script to point at a different installer.",
		RunE: func(cmd *cobra.Command, args []string) error {
			url := script
			if url == "" {
				url = installScriptURL
			}
			fmt.Printf("current: %s\n", version.String())
			fmt.Printf("updating via %s ...\n", url)
			c := exec.Command("sh", "-c", "curl -fsSL '"+url+"' | sh")
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			c.Env = os.Environ()
			if err := c.Run(); err != nil {
				return fmt.Errorf("installer failed: %w", err)
			}
			if st := service.Current(); st.Installed {
				if err := service.Restart(); err != nil {
					fmt.Printf("note: could not restart the service automatically: %v\n", err)
				} else {
					fmt.Println("restarted the service")
				}
			}
			fmt.Println("done — run 'vector doctor' to verify")
			return nil
		},
	}
	cmd.Flags().StringVar(&script, "script", "", "installer URL (default: the official install.sh)")
	return cmd
}
