package cli

import (
	"fmt"
	"syscall"
)

// reloadGateway asks a running gateway to re-read its configuration (SIGHUP).
// It is a no-op when nothing is running, so it is safe to call after any config
// write.
func reloadGateway() {
	cfg, err := loadConfig()
	if err != nil {
		return
	}
	pid, err := readPID(cfg)
	if err != nil || !alive(pid) {
		return
	}
	if err := syscall.Kill(pid, syscall.SIGHUP); err == nil {
		fmt.Println("reloaded the running gateway")
	}
}
