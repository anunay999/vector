package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/anunay999/vector/internal/registry"
	"github.com/spf13/cobra"
)

func newModelsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List configured roles and models",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			reg := registry.New(cfg)
			if asJSON {
				payload := map[string]any{
					"roles":  cfg.Roles,
					"models": reg.Entries(),
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(payload)
			}

			fmt.Println("Roles:")
			w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "  ROLE\tTIER\tPREFER")
			for _, name := range cfg.RoleNames() {
				r := cfg.Roles[name]
				fmt.Fprintf(w, "  %s\t%s\t%v\n", name, r.Tier, r.Prefer)
			}
			w.Flush()

			fmt.Println("\nModels:")
			w = tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "  ID\tCTX\t$IN\t$OUT\tTAGS")
			for _, e := range reg.Entries() {
				fmt.Fprintf(w, "  %s\t%d\t%.3f\t%.3f\t%v\n", e.ID, e.Context, e.Price.In, e.Price.Out, e.Tags)
			}
			w.Flush()
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}
