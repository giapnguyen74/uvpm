// Package startup installs the daemon as a systemd *user* service.
package startup

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

const unitName = "uvpmd.service"

func unitPath() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".config", "systemd", "user", unitName), nil
}

// Unit renders the service file for the given uvpm binary.
func Unit(exe, uvpmHome string) string {
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=uvpm daemon\nAfter=network.target\n\n[Service]\nType=simple\n")
	fmt.Fprintf(&b, "ExecStart=%s daemon\n", exe)
	b.WriteString("Restart=on-failure\n")
	b.WriteString("# Stop only the daemon: apps keep running and the next daemon adopts them.\nKillMode=process\nTimeoutStopSec=30\n")
	b.WriteString("Environment=PATH=%h/.local/bin:%h/.cargo/bin:/usr/local/bin:/usr/bin:/bin\n")
	if uvpmHome != "" {
		fmt.Fprintf(&b, "Environment=UVPM_HOME=%s\n", uvpmHome)
	}
	b.WriteString("\n[Install]\nWantedBy=default.target\n")
	return b.String()
}

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl --user %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func check() error {
	if runtime.GOOS != "linux" {
		return errors.New("startup is only supported on Linux (systemd) and macOS (launchd)")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return errors.New("systemctl not found")
	}
	return nil
}

// Active reports whether the user unit is running.
func Active() bool {
	if check() != nil {
		return false
	}
	return exec.Command("systemctl", "--user", "is-active", "--quiet", unitName).Run() == nil
}

// Restart restarts the unit; systemd's SIGTERM makes the daemon drain.
func Restart() error { return systemctl("restart", unitName) }

// Install writes the unit, enables and starts it. It returns advice to print.
func Install(exe, uvpmHome string) (advice []string, err error) {
	if runtime.GOOS == "darwin" {
		return installLaunchd(exe, uvpmHome)
	}
	if err := check(); err != nil {
		return nil, err
	}
	p, err := unitPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, []byte(Unit(exe, uvpmHome)), 0o644); err != nil {
		return nil, err
	}
	if err := systemctl("daemon-reload"); err != nil {
		return nil, err
	}
	if err := systemctl("enable", "--now", unitName); err != nil {
		return nil, err
	}
	advice = append(advice, "installed "+p)
	if u, err := user.Current(); err == nil {
		out, _ := exec.Command("loginctl", "show-user", u.Username, "-p", "Linger").Output()
		if !strings.Contains(string(out), "Linger=yes") {
			advice = append(advice, "linger is off: the daemon will only start after you log in; to start at boot run: loginctl enable-linger "+u.Username)
		}
	}
	return advice, nil
}

// Uninstall disables the unit and removes the file. Apps keep running.
func Uninstall() error {
	if runtime.GOOS == "darwin" {
		return uninstallLaunchd()
	}
	if err := check(); err != nil {
		return err
	}
	p, err := unitPath()
	if err != nil {
		return err
	}
	_ = systemctl("disable", "--now", unitName)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return systemctl("daemon-reload")
}
