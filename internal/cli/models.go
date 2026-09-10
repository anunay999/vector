package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/anunay999/vector/internal/registry"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newModelsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "models",
		Short: "List and edit the model registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			reg := registry.New(cfg)
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"roles": cfg.Roles, "models": reg.Entries()})
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
	cmd.AddCommand(newModelsSetCmd(), newModelsRemoveCmd(), newModelsUseCmd())
	return cmd
}

func newModelsSetCmd() *cobra.Command {
	var tags string
	var context int
	var priceIn, priceOut float64
	cmd := &cobra.Command{
		Use:   "set <provider/model> [--tags a,b] [--context N] [--in F] [--out F]",
		Short: "Add or update a model in the registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !strings.Contains(id, "/") {
				return fmt.Errorf("model id must be namespaced as provider/model")
			}
			path := configPath()
			m, err := loadRawConfigMap(path)
			if err != nil {
				return err
			}
			models := anyList(m, "models")
			entry, idx := findModelEntry(models, id)
			if entry == nil {
				entry = map[string]any{"id": id}
				models = append(models, entry)
			} else {
				models[idx] = entry
			}
			entry["id"] = id
			if cmd.Flags().Changed("tags") {
				entry["tags"] = splitTags(tags)
			}
			if cmd.Flags().Changed("context") {
				entry["context"] = context
			}
			price, _ := entry["price"].(map[string]any)
			if price == nil {
				price = map[string]any{}
			}
			if cmd.Flags().Changed("in") {
				price["in"] = priceIn
			}
			if cmd.Flags().Changed("out") {
				price["out"] = priceOut
			}
			entry["price"] = price
			m["models"] = models
			if err := writeRawConfigMap(path, m); err != nil {
				return err
			}
			fmt.Printf("set model %s\n", id)
			reloadGateway()
			return nil
		},
	}
	cmd.Flags().StringVar(&tags, "tags", "", "comma-separated capability tags")
	cmd.Flags().IntVar(&context, "context", 0, "context window in tokens")
	cmd.Flags().Float64Var(&priceIn, "in", 0, "USD per million input tokens")
	cmd.Flags().Float64Var(&priceOut, "out", 0, "USD per million output tokens")
	return cmd
}

func newModelsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <provider/model>",
		Short: "Remove a model from the registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			path := configPath()
			m, err := loadRawConfigMap(path)
			if err != nil {
				return err
			}
			models := anyList(m, "models")
			kept := models[:0]
			removed := false
			for _, item := range models {
				if e, ok := item.(map[string]any); ok {
					if s, _ := e["id"].(string); s == id {
						removed = true
						continue
					}
				}
				kept = append(kept, item)
			}
			if !removed {
				return fmt.Errorf("model %q is not in the registry", id)
			}
			m["models"] = kept
			if err := writeRawConfigMap(path, m); err != nil {
				return err
			}
			fmt.Printf("removed model %s\n", id)
			reloadGateway()
			return nil
		},
	}
}

func newModelsUseCmd() *cobra.Command {
	var appendPrefer bool
	cmd := &cobra.Command{
		Use:   "use <role> <target> [--append]",
		Short: "Set (or append) a role's preferred target; e.g. use worker openrouter/z-ai/glm-5.3-flash",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			role, target := args[0], args[1]
			path := configPath()
			m, err := loadRawConfigMap(path)
			if err != nil {
				return err
			}
			roles, _ := m["roles"].(map[string]any)
			entry, ok := roles[role].(map[string]any)
			if !ok {
				return fmt.Errorf("no role %q in config.yaml", role)
			}
			if appendPrefer {
				prefer, _ := entry["prefer"].([]any)
				entry["prefer"] = append(prefer, target)
			} else {
				entry["prefer"] = []any{target}
			}
			if err := writeRawConfigMap(path, m); err != nil {
				return err
			}
			fmt.Printf("role %s -> %s\n", role, target)
			reloadGateway()
			return nil
		},
	}
	cmd.Flags().BoolVar(&appendPrefer, "append", false, "append as a fallback instead of replacing")
	return cmd
}

// anyList returns m[key] as a []any, creating it when missing.
func anyList(m map[string]any, key string) []any {
	if v, ok := m[key].([]any); ok {
		return v
	}
	return []any{}
}

func findModelEntry(models []any, id string) (map[string]any, int) {
	for i, item := range models {
		e, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if s, _ := e["id"].(string); s == id {
			return e, i
		}
	}
	return nil, -1
}

func splitTags(s string) []any {
	var out []any
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			out = append(out, t)
		}
	}
	if out == nil {
		out = []any{}
	}
	return out
}

func writeRawConfigMap(path string, m map[string]any) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", data, 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
