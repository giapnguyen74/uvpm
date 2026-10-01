package startup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const launchdLabel = "com.uvpm.daemon"

// NeedsApproval reports whether `startup` must show its plan and ask first.
// On macOS it installs a launchd agent that runs at every login, so the user
// has to opt in explicitly.
func NeedsApproval() bool { return runtime.GOOS == "darwin" }

func plistPath() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "Library", "LaunchAgents", launchdLabel+".plist"), nil
}

func xml(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// Plist renders the launchd agent for the given uvpm binary.
func Plist(exe, uvpmHome, logPath string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>` + launchdLabel + `</string>
	<key>ProgramArguments</key>
	<array><string>` + xml(exe) + `</string><string>daemon</string></array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
	<key>AbandonProcessGroup</key><true/>
	<key>StandardOutPath</key><string>` + xml(logPath) + `</string>
	<key>StandardErrorPath</key><string>` + xml(logPath) + `</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key><string>` + xml(os.Getenv("HOME")) + `/.local/bin:` + xml(os.Getenv("HOME")) + `/.cargo/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
`)
	if uvpmHome != "" {
		b.WriteString("\t\t<key>UVPM_HOME</key><string>" + xml(uvpmHome) + "</string>\n")
	}
	b.WriteString("\t</dict>\n</dict>\n</plist>\n")
	return b.String()
}

func domain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

func launchctl(args ...string) error {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Describe lists, in plain words, everything Install would change.
func Describe(exe, uvpmHome string) []string {
	switch runtime.GOOS {
	case "darwin":
		p, _ := plistPath()
		return []string{
			"write the launchd agent " + p,
			"the agent runs `" + exe + " daemon` at every login and restarts it if it crashes",
			"daemon output goes to " + filepath.Join(uvpmHomeOrDefault(uvpmHome), "daemon.log"),
			"run `launchctl bootstrap " + domain() + " " + p + "` to load it now",
			"a daemon already running is handed over first; its apps keep running and are adopted",
			"undo anytime with `uvpm shutdown`",
		}
	case "linux":
		p, _ := unitPath()
		return []string{
			"write the systemd user unit " + p,
			"run `systemctl --user daemon-reload` and `systemctl --user enable --now " + unitName + "`",
		}
	}
	return nil
}

func uvpmHomeOrDefault(h string) string {
	if h != "" {
		return h
	}
	d, _ := os.UserHomeDir()
	return filepath.Join(d, ".uvpm")
}

func installLaunchd(exe, uvpmHome string) ([]string, error) {
	p, err := plistPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	logPath := filepath.Join(uvpmHomeOrDefault(uvpmHome), "daemon.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, []byte(Plist(exe, uvpmHome, logPath)), 0o644); err != nil {
		return nil, err
	}
	_ = launchctl("bootout", domain()+"/"+launchdLabel) // reload if already loaded
	if err := launchctl("bootstrap", domain(), p); err != nil {
		return nil, err
	}
	return []string{"installed " + p}, nil
}

func uninstallLaunchd() error {
	p, err := plistPath()
	if err != nil {
		return err
	}
	_ = launchctl("bootout", domain()+"/"+launchdLabel)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
