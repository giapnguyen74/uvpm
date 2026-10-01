// Package home resolves the uvpm work folder (~/.uvpm, or $UVPM_HOME).
package home

import (
	"os"
	"path/filepath"
)

func Dir() string {
	if v := os.Getenv("UVPM_HOME"); v != "" {
		return v
	}
	h, err := os.UserHomeDir()
	if err != nil {
		h = "."
	}
	return filepath.Join(h, ".uvpm")
}

func Sock() string      { return filepath.Join(Dir(), "uvpm.sock") }
func LockFile() string  { return filepath.Join(Dir(), "uvpm.lock") }
func StateFile() string { return filepath.Join(Dir(), "state.json") }
func EnvFile() string   { return filepath.Join(Dir(), "env") }
func DumpFile() string  { return filepath.Join(Dir(), "dump.json") }
func LogsDir() string   { return filepath.Join(Dir(), "logs") }
func DaemonLog() string { return filepath.Join(Dir(), "daemon.log") }

// Ensure creates the work folder layout.
// The folder is always made private to the current user, even when it already
// existed with looser permissions: it holds the daemon socket and the saved
// environments.
func Ensure() error {
	if err := os.MkdirAll(LogsDir(), 0o700); err != nil {
		return err
	}
	return os.Chmod(Dir(), 0o700)
}
