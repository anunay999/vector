package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/anunay999/vector/internal/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect and edit configuration"}

	var force bool
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Write a default configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := configPath()
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", path)
			}
			if err := config.Write(config.Default(), path); err != nil {
				return err
			}
			fmt.Printf("wrote %s\n", path)
			return nil
		},
	}
	initCmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config")

	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if cfg.Path() != "" {
				fmt.Fprintf(os.Stderr, "# %s\n", cfg.Path())
			}
			data, err := yaml.Marshal(cfg)
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(data)
			return err
		},
	}

	pathCmd := &cobra.Command{
		Use:   "path",
		Short: "Print the active config file path",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(configPath())
		},
	}

	getCmd := &cobra.Command{
		Use:   "get <path>",
		Short: "Read a value by dotted path (e.g. budget.daily_usd)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := loadRawConfigMap(configPath())
			if err != nil {
				return err
			}
			v, err := getPath(m, strings.Split(args[0], "."))
			if err != nil {
				return err
			}
			out, _ := yaml.Marshal(v)
			fmt.Print(string(out))
			return nil
		},
	}

	setCmd := &cobra.Command{
		Use:   "set <path> <value>",
		Short: "Set a value by dotted path (e.g. budget.daily_usd 10)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := configPath()
			m, err := loadRawConfigMap(path)
			if err != nil {
				return err
			}
			if err := setPath(m, strings.Split(args[0], "."), coerce(args[1])); err != nil {
				return err
			}
			data, err := yaml.Marshal(m)
			if err != nil {
				return err
			}
			if err := os.WriteFile(path+".tmp", data, 0o600); err != nil {
				return err
			}
			if err := os.Rename(path+".tmp", path); err != nil {
				return err
			}
			fmt.Printf("%s = %s\n", args[0], args[1])
			return nil
		},
	}

	validateCmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate the configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			fmt.Println("config ok:", cfg.String())
			if missing := cfg.UnresolvedVars(); len(missing) > 0 {
				fmt.Printf("warning: unresolved env vars: %v\n", missing)
			}
			return nil
		},
	}
	cmd.AddCommand(initCmd, showCmd, pathCmd, getCmd, setCmd, validateCmd)
	return cmd
}

// configPath returns the config path to operate on.
func configPath() string {
	if cfgPath != "" {
		return cfgPath
	}
	return config.DefaultPath()
}

// loadRawConfigMap loads a config as a generic map for path-based editing. A
// missing file starts from the built-in default so `set` produces a complete
// config.
func loadRawConfigMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			data, err = yaml.Marshal(config.Default())
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func getPath(m map[string]any, path []string) (any, error) {
	var cur any = m
	for _, p := range path {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[p]
			if !ok {
				return nil, fmt.Errorf("no such key: %s", strings.Join(path, "."))
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(node) {
				return nil, fmt.Errorf("bad list index %q", p)
			}
			cur = node[i]
		default:
			return nil, fmt.Errorf("cannot descend into %q", p)
		}
	}
	return cur, nil
}

func setPath(m map[string]any, path []string, value any) error {
	if len(path) == 0 {
		return fmt.Errorf("empty path")
	}
	var cur any = m
	for i, p := range path {
		last := i == len(path)-1
		switch node := cur.(type) {
		case map[string]any:
			if last {
				node[p] = value
				return nil
			}
			next, ok := node[p]
			if !ok || next == nil {
				child := map[string]any{}
				node[p] = child
				cur = child
				continue
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(p)
			if err != nil || idx < 0 || idx >= len(node) {
				return fmt.Errorf("bad list index %q", p)
			}
			if last {
				node[idx] = value
				return nil
			}
			cur = node[idx]
		default:
			return fmt.Errorf("cannot descend into %q", p)
		}
	}
	return nil
}

// coerce turns a CLI string into a typed YAML value.
func coerce(s string) any {
	switch s {
	case "true":
		return true
	case "false":
		return false
	case "null", "~":
		return nil
	}
	if i, err := strconv.Atoi(s); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	if strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{") {
		var v any
		if yaml.Unmarshal([]byte(s), &v) == nil {
			return v
		}
	}
	return s
}
