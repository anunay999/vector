package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/anunay999/vector/internal/config"
	"github.com/spf13/cobra"
)

func writePID(cfg *config.Config) error {
	if err := os.MkdirAll(filepath.Dir(cfg.PIDFile()), 0o700); err != nil {
		return err
	}
	return os.WriteFile(cfg.PIDFile(), []byte(strconv.Itoa(os.Getpid())), 0o600)
}

func removePID(cfg *config.Config) { _ = os.Remove(cfg.PIDFile()) }

func readPID(cfg *config.Config) (int, error) {
	data, err := os.ReadFile(cfg.PIDFile())
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid pidfile: %w", err)
	}
	return pid, nil
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func healthURL(cfg *config.Config) string {
	addr := cfg.Listen.Anthropic
	if !strings.HasPrefix(addr, "http") {
		addr = "http://" + addr
	}
	return strings.TrimRight(addr, "/") + "/healthz"
}

func newUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Start the gateway in the background",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if pid, err := readPID(cfg); err == nil && alive(pid) {
				fmt.Printf("vector already running (pid %d)\n", pid)
				return nil
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(cfg.LogDir(), 0o700); err != nil {
				return err
			}
			logf, err := os.OpenFile(filepath.Join(cfg.LogDir(), "gateway.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				return err
			}
			defer logf.Close()

			argv := []string{}
			if cfgPath != "" {
				argv = append(argv, "--config", cfgPath)
			}
			argv = append(argv, "serve")
			c := exec.Command(exe, argv...)
			c.Stdout = logf
			c.Stderr = logf
			c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := c.Start(); err != nil {
				return err
			}
			if err := writePID(cfg); err != nil {
				return err
			}
			_ = os.WriteFile(cfg.PIDFile(), []byte(strconv.Itoa(c.Process.Pid)), 0o600)

			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if probeHealth(cfg) {
					fmt.Printf("vector up (pid %d) on %s\n", c.Process.Pid, cfg.Listen.Anthropic)
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}
			return fmt.Errorf("gateway did not become healthy; see %s", filepath.Join(cfg.LogDir(), "gateway.log"))
		},
	}
}

func newDownCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop the background gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			return stopGateway()
		},
	}
}

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the background gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := stopGateway(); err != nil {
				return err
			}
			return newUpCmd().RunE(cmd, args)
		},
	}
}

// stopGateway terminates the running gateway if any.
func stopGateway() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	pid, err := readPID(cfg)
	if err != nil {
		fmt.Println("vector is not running")
		return nil
	}
	if alive(pid) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && alive(pid) {
			time.Sleep(100 * time.Millisecond)
		}
	}
	removePID(cfg)
	fmt.Println("vector down")
	return nil
}

func newStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show gateway and harness status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			pid, perr := readPID(cfg)
			running := perr == nil && alive(pid)

			out := statusOut{
				GatewayRunning: running,
				Anthropic:      cfg.Listen.Anthropic,
				OpenAI:         cfg.Listen.OpenAI,
				RoutingEnabled: cfg.RoutingEnabled,
			}
			if running {
				out.PID = pid
				if st, serr := fetchStatus(cfg); serr == nil {
					out.SpendUSD = st.SpendUSD
					out.PerProvider = st.PerProvider
				}
			}
			for _, a := range adapters(cfg) {
				hs := harnessState{Name: a.Name()}
				if s, serr := a.Status(); serr == nil {
					hs.Enabled = s.Enabled
					hs.Detail = s.Detail
				}
				out.Harnesses = append(out.Harnesses, hs)
			}

			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			if running {
				fmt.Printf("Gateway: up (pid %d)\n", pid)
			} else {
				fmt.Println("Gateway: down")
			}
			fmt.Printf("  anthropic: %s\n  openai:    %s\n", cfg.Listen.Anthropic, cfg.Listen.OpenAI)
			fmt.Printf("  routing:   %v\n", cfg.RoutingEnabled)
			if running {
				fmt.Printf("  spend:     $%.4f today\n", out.SpendUSD)
				for p, v := range out.PerProvider {
					fmt.Printf("    %-18s $%.4f\n", p, v)
				}
			}
			fmt.Println("Harnesses:")
			for _, h := range out.Harnesses {
				mark := "off"
				if h.Enabled {
					mark = "on"
				}
				fmt.Printf("  %-12s %-3s %s\n", h.Name, mark, h.Detail)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}

type statusOut struct {
	GatewayRunning bool               `json:"gateway_running"`
	PID            int                `json:"pid,omitempty"`
	Anthropic      string             `json:"anthropic"`
	OpenAI         string             `json:"openai"`
	RoutingEnabled bool               `json:"routing_enabled"`
	SpendUSD       float64            `json:"spend_usd"`
	PerProvider    map[string]float64 `json:"per_provider,omitempty"`
	Harnesses      []harnessState     `json:"harnesses"`
}

type statusPayload struct {
	RoutingEnabled bool               `json:"routing_enabled"`
	SpendUSD       float64            `json:"spend_usd"`
	PerProvider    map[string]float64 `json:"per_provider"`
}

func fetchStatus(cfg *config.Config) (statusPayload, error) {
	var out statusPayload
	addr := cfg.Listen.Anthropic
	if !strings.HasPrefix(addr, "http") {
		addr = "http://" + addr
	}
	url := strings.TrimRight(addr, "/") + "/admin/status"
	resp, err := http.Get(url)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out, err
}

func probeHealth(cfg *config.Config) bool {
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(healthURL(cfg))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
