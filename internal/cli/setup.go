package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/anunay999/vector/internal/config"
	"github.com/spf13/cobra"
)

type setupResult struct {
	ConfigPath  string   `json:"config_path"`
	Provider    string   `json:"provider"`
	KeyStored   bool     `json:"key_stored"`
	Wired       []string `json:"wired"`
	Started     bool     `json:"started"`
	Checks      []check  `json:"checks"`
	NextActions []string `json:"next_actions"`
	OK          bool     `json:"ok"`
}

func newSetupCmd() *cobra.Command {
	var (
		key      string
		provider string
		wire     string
		start    bool
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Configure vector in one non-interactive step",
		Long: "Idempotent setup for agents and scripts: writes a config if missing, stores\n" +
			"the provider key in the private env file, optionally wires harnesses and\n" +
			"starts the gateway, then verifies everything. Safe to re-run.",
		RunE: func(cmd *cobra.Command, args []string) error {
			res := setupResult{Provider: provider}

			// 1. Config (keep an existing one).
			path := configPath()
			if _, err := os.Stat(path); err != nil {
				if werr := config.Write(config.Default(), path); werr != nil {
					return werr
				}
			}
			res.ConfigPath = path

			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if _, ok := cfg.ProviderByID(provider); !ok {
				return fmt.Errorf("provider %q is not configured; add it first, e.g.\n"+
					"  vector config set providers.%d.id %s\n"+
					"  vector config set providers.%d.type openai_compatible\n"+
					"  vector config set providers.%d.base_url <url>",
					provider, len(cfg.Providers), provider, len(cfg.Providers), len(cfg.Providers))
			}

			// 2. Secret.
			if key == "" {
				if env, _ := config.ReadEnv(); env[envKeyFor(provider)] != "" {
					key = env[envKeyFor(provider)]
				} else if v := os.Getenv(envKeyFor(provider)); v != "" {
					key = v
				}
			}
			if key != "" {
				if err := config.SetEnv(envKeyFor(provider), key); err != nil {
					return err
				}
				res.KeyStored = true
			}

			// 3. Wire harnesses.
			if wire != "" {
				res.Wired = wireHarnesses(wire)
			}

			// 4. Start.
			if start {
				if err := newUpCmd().RunE(cmd, nil); err != nil {
					return err
				}
				res.Started = true
			}

			// 5. Verify.
			if c, err := loadConfig(); err == nil {
				res.Checks = runChecks(c)
			}
			for _, ch := range res.Checks {
				switch ch.Status {
				case "fail", "warn":
					res.NextActions = append(res.NextActions, nextActionFor(ch))
				}
			}
			res.OK = true
			for _, ch := range res.Checks {
				if ch.Status == "fail" {
					res.OK = false
				}
			}

			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}
			fmt.Printf("config: %s\n", res.ConfigPath)
			fmt.Printf("provider: %s (key stored: %v)\n", res.Provider, res.KeyStored)
			if len(res.Wired) > 0 {
				fmt.Printf("wired: %s\n", strings.Join(res.Wired, ", "))
			}
			if res.Started {
				fmt.Println("gateway: started")
			}
			fmt.Println("checks:")
			for _, ch := range res.Checks {
				fmt.Printf("  %-4s %-22s %s\n", ch.Status, ch.Name, ch.Detail)
			}
			if len(res.NextActions) > 0 {
				fmt.Println("next:")
				for _, a := range res.NextActions {
					fmt.Printf("  - %s\n", a)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "provider API key to store")
	cmd.Flags().StringVar(&provider, "provider", "openrouter", "provider id to configure")
	cmd.Flags().StringVar(&wire, "wire", "", "harnesses to wire: claude,codex,opencode,all,none")
	cmd.Flags().BoolVar(&start, "start", false, "start the gateway after configuring")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}

// envKeyFor maps a provider id to its conventional env variable name.
func envKeyFor(provider string) string {
	p := strings.ToUpper(strings.ReplaceAll(provider, "-", "_"))
	return p + "_API_KEY"
}

func nextActionFor(c check) string {
	if strings.HasPrefix(c.Name, "harness/") {
		return "vector " + shortName(strings.TrimPrefix(c.Name, "harness/")) + " on"
	}
	switch c.Name {
	case "env":
		return "vector env set <VAR> <value>"
	case "gateway":
		return "vector up"
	case "service":
		return "vector service install"
	case "config":
		return "fix the config and re-run: vector config validate"
	}
	return c.Name + ": " + c.Detail
}
