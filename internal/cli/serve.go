package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/anunay999/vector/internal/budget"
	"github.com/anunay999/vector/internal/gateway"
	"github.com/anunay999/vector/internal/telemetry"
	"github.com/spf13/cobra"
)

func newServeCmd() *cobra.Command {
	var verbose bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the gateway in the foreground",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			level := slog.LevelInfo
			if verbose {
				level = slog.LevelDebug
			}
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

			rec := telemetry.New(cfg.TelemetryDir(), cfg.Telemetry.Enabled)
			gov := budget.New(cfg.Budget.DailyUSD, cfg.Budget.PerProvider, cfg.Budget.OnBreach, cfg.Budget.MaxConcurrentSubagentsPerHarness)
			srv := gateway.New(cfg, rec, gov, logger)

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			if err := writePID(cfg); err != nil {
				logger.Warn("could not write pidfile", "err", err)
			}
			defer removePID(cfg)

			addrs := []string{cfg.Listen.Anthropic}
			if cfg.Listen.OpenAI != cfg.Listen.Anthropic {
				addrs = append(addrs, cfg.Listen.OpenAI)
			}
			errCh := make(chan error, len(addrs))
			for _, addr := range addrs {
				addr := addr
				go func() { errCh <- srv.ListenAndServe(ctx, addr) }()
			}
			fmt.Fprintf(os.Stderr, "vector gateway up on %v (routing=%v)\n", addrs, cfg.RoutingEnabled)

			select {
			case <-ctx.Done():
				return nil
			case err := <-errCh:
				if err != nil && !errors.Is(err, context.Canceled) {
					return err
				}
				return nil
			}
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "debug logging")
	return cmd
}
