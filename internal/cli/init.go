package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/anunay999/vector/internal/config"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var (
		force bool
		key   string
		wire  string
		yes   bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up vector: config, secrets, and optional harness wiring",
		Long: "Init writes a configuration, stores your OpenRouter key in the private env\n" +
			"file, and can wire your coding harnesses, then verifies everything.",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := configPath()
			reader := bufio.NewReader(os.Stdin)

			// 1. Config.
			if _, err := os.Stat(path); err == nil && !force {
				fmt.Printf("config already exists: %s (use --force to overwrite)\n", path)
			} else {
				if err := config.Write(config.Default(), path); err != nil {
					return err
				}
				fmt.Printf("✓ wrote config %s\n", path)
			}

			// 2. Provider key.
			if key == "" {
				if existing, _ := config.ReadEnv(); existing["OPENROUTER_API_KEY"] != "" {
					key = existing["OPENROUTER_API_KEY"]
				} else if env := os.Getenv("OPENROUTER_API_KEY"); env != "" {
					key = env
				} else if !yes {
					key = prompt(reader, "OpenRouter API key (sk-or-...), blank to skip", "")
				}
			}
			if key != "" {
				if err := config.SetEnv("OPENROUTER_API_KEY", key); err != nil {
					return err
				}
				fmt.Printf("✓ stored OPENROUTER_API_KEY in %s\n", config.EnvPath())
			} else {
				fmt.Printf("! no OpenRouter key stored; set it later with: vector env set OPENROUTER_API_KEY sk-or-...\n")
			}

			// 3. Harness wiring.
			if wire == "" && !yes {
				wire = prompt(reader, "Wire harnesses now? [claude,codex,opencode,none]", "none")
			}
			wired := wireHarnesses(wire)

			// 4. Summary.
			fmt.Println()
			fmt.Println("Next steps:")
			fmt.Println("  vector up        # start the gateway")
			fmt.Println("  vector doctor    # verify config, keys, and wiring")
			fmt.Println("  vector spend     # see off-plan usage")
			if len(wired) == 0 {
				fmt.Println("  vector claude on # wire a harness later")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config")
	cmd.Flags().StringVar(&key, "key", "", "OpenRouter API key to store")
	cmd.Flags().StringVar(&wire, "wire", "", "comma list of harnesses to wire: claude,codex,opencode,all,none")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "non-interactive: accept defaults")
	return cmd
}

func wireHarnesses(spec string) []string {
	spec = strings.TrimSpace(strings.ToLower(spec))
	if spec == "" || spec == "none" {
		return nil
	}
	all := []string{"claude", "codex", "opencode"}
	if spec == "all" {
		spec = strings.Join(all, ",")
	}
	want := map[string]bool{}
	for _, s := range strings.Split(spec, ",") {
		want[strings.TrimSpace(s)] = true
	}
	cfg, err := loadConfig()
	if err != nil {
		fmt.Printf("! could not load config for wiring: %v\n", err)
		return nil
	}
	var done []string
	for _, a := range adapters(cfg) {
		short := shortName(a.Name())
		if !want[short] {
			continue
		}
		if _, err := a.Enable(); err != nil {
			fmt.Printf("! %s: %v\n", short, err)
			continue
		}
		fmt.Printf("✓ wired %s\n", short)
		done = append(done, short)
	}
	return done
}

func prompt(r *bufio.Reader, question, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", question, def)
	} else {
		fmt.Printf("%s: ", question)
	}
	line, err := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if err != nil || line == "" {
		return def
	}
	return line
}
