// Package service installs vector as a user-level background service so the
// gateway survives reboots: a launchd agent on macOS and a systemd user unit on
// Linux.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const label = "io.vector.gateway"

// Status describes the installed service.
type Status struct {
	Platform  string
	Path      string
	Installed bool
	Active    bool
	Detail    string
}

// Install writes and loads the user service. configPathArg is passed to
// `vector serve --config`, and logPath receives the gateway's stdout/stderr so
// every start method logs to the same place.
func Install(configPathArg, logPath string) (Status, error) {
	exe, err := os.Executable()
	if err != nil {
		return Status{}, err
	}
	switch runtime.GOOS {
	case "darwin":
		return installLaunchd(exe, configPathArg, logPath)
	case "linux":
		return installSystemd(exe, configPathArg, logPath)
	default:
		return Status{Platform: runtime.GOOS}, fmt.Errorf("service install is not supported on %s", runtime.GOOS)
	}
}

// Uninstall stops and removes the user service.
func Uninstall() (Status, error) {
	switch runtime.GOOS {
	case "darwin":
		path := launchdPath()
		_ = run("launchctl", "unload", "-w", path)
		err := os.Remove(path)
		return Status{Platform: "darwin", Path: path, Installed: false, Detail: "unloaded and removed"}, ignoreNotExist(err)
	case "linux":
		_ = run("systemctl", "--user", "disable", "--now", "vector.service")
		path := systemdPath()
		err := os.Remove(path)
		_ = run("systemctl", "--user", "daemon-reload")
		return Status{Platform: "linux", Path: path, Installed: false, Detail: "disabled and removed"}, ignoreNotExist(err)
	default:
		return Status{Platform: runtime.GOOS}, fmt.Errorf("service uninstall is not supported on %s", runtime.GOOS)
	}
}

// Status reports whether the service is installed and active.
func Current() Status {
	switch runtime.GOOS {
	case "darwin":
		path := launchdPath()
		st := Status{Platform: "darwin", Path: path, Installed: fileExists(path)}
		if st.Installed {
			st.Active = run("launchctl", "list", label) == nil
		}
		return st
	case "linux":
		path := systemdPath()
		st := Status{Platform: "linux", Path: path, Installed: fileExists(path)}
		if st.Installed {
			st.Active = run("systemctl", "--user", "is-active", "--quiet", "vector.service") == nil
		}
		return st
	default:
		return Status{Platform: runtime.GOOS}
	}
}

func installLaunchd(exe, configPathArg, logPath string) (Status, error) {
	path := launchdPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Status{}, err
	}
	_ = os.MkdirAll(filepath.Dir(logPath), 0o700)

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>--config</string>
    <string>%s</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, label, xmlEscape(exe), xmlEscape(configPathArg),
		xmlEscape(logPath), xmlEscape(logPath))

	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return Status{}, err
	}
	_ = run("launchctl", "unload", "-w", path)
	if err := run("launchctl", "load", "-w", path); err != nil {
		return Status{Platform: "darwin", Path: path, Installed: true}, fmt.Errorf("launchctl load: %w", err)
	}
	return Status{Platform: "darwin", Path: path, Installed: true, Active: true, Detail: "loaded"}, nil
}

func installSystemd(exe, configPathArg, logPath string) (Status, error) {
	path := systemdPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Status{}, err
	}
	_ = os.MkdirAll(filepath.Dir(logPath), 0o700)
	unit := fmt.Sprintf(`[Unit]
Description=vector subagent model router
After=network-online.target

[Service]
ExecStart=%s --config %s serve
Restart=on-failure
RestartSec=3
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, exe, configPathArg, logPath, logPath)
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return Status{}, err
	}
	_ = run("systemctl", "--user", "daemon-reload")
	if err := run("systemctl", "--user", "enable", "--now", "vector.service"); err != nil {
		return Status{Platform: "linux", Path: path, Installed: true}, fmt.Errorf("systemctl enable: %w", err)
	}
	return Status{Platform: "linux", Path: path, Installed: true, Active: true, Detail: "enabled"}, nil
}

func launchdPath() string {
	return filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents", label+".plist")
}

func systemdPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(base, "systemd", "user", "vector.service")
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ignoreNotExist(err error) error {
	if err != nil && os.IsNotExist(err) {
		return nil
	}
	return err
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
