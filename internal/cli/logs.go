package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	var follow, showPath bool
	var lines int
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show or follow the gateway log",
		Long: "Prints the gateway log. Every start method writes the same file:\n" +
			"`vector up`, `vector serve`, and the installed service all log to\n" +
			"~/.config/vector/logs/gateway.log.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			path := filepath.Join(cfg.LogDir(), "gateway.log")
			if showPath {
				fmt.Println(path)
				return nil
			}
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("no log yet at %s (start the gateway with 'vector up')", path)
			}
			if follow {
				ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
				defer stop()
				fmt.Fprintf(os.Stderr, "following %s (ctrl-c to stop)\n", path)
				return followFile(ctx, func() string { return path }, os.Stdout)
			}
			return printLastLines(path, lines, os.Stdout)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "stream new log lines")
	cmd.Flags().IntVarP(&lines, "lines", "n", 200, "number of trailing lines to print")
	cmd.Flags().BoolVar(&showPath, "path", false, "print the log file path")
	return cmd
}
