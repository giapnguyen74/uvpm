// Package model holds the types shared by the CLI and the daemon.
package model

import (
	"fmt"
	"regexp"
	"time"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidName checks an app name. Names end up in log file names, so path
// separators and leading dots are not allowed.
func ValidName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid app name %q: use 1-64 letters, digits, '.', '_' or '-', starting with a letter or digit", name)
	}
	return nil
}

// Spec describes an app. It is stored as given; the command line is resolved
// from it on every (re)start so project changes are picked up.
type Spec struct {
	Name    string            `json:"name"`
	Target  string            `json:"target"`
	Args    []string          `json:"args,omitempty"`
	Mode    string            `json:"mode,omitempty"` // "", uv, script, raw
	Cwd     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	EnvFile string            `json:"env_file,omitempty"`
	// BaseEnv is the environment of the shell that ran `uvpm run`. When set it
	// replaces the daemon's own environment as the base layer.
	BaseEnv map[string]string `json:"base_env,omitempty"`

	// uv options
	Entry  string   `json:"entry,omitempty"`  // [project.scripts] name
	Module string   `json:"module,omitempty"` // python -m <module>
	Python string   `json:"python,omitempty"`
	With   []string `json:"with,omitempty"`
	Groups []string `json:"groups,omitempty"`
	Extras []string `json:"extras,omitempty"`
	Frozen bool     `json:"frozen,omitempty"`
	NoUV   bool     `json:"no_uv,omitempty"`

	// raw options
	Shell bool `json:"shell,omitempty"`

	// supervision
	Autorestart    bool `json:"autorestart"`
	MaxRestarts    int  `json:"max_restarts"` // consecutive; 0 = unlimited
	RestartDelayMs int  `json:"restart_delay_ms"`
	KillTimeoutMs  int  `json:"kill_timeout_ms"`
}

// App statuses.
const (
	StatusOnline     = "online"
	StatusStopped    = "stopped"
	StatusExited     = "exited"
	StatusErrored    = "errored"
	StatusRestarting = "restarting"
	StatusSyncing    = "syncing"
)

// AppInfo is the runtime view of an app.
type AppInfo struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Mode      string    `json:"mode"`
	Status    string    `json:"status"`
	Desired   string    `json:"desired"`
	Pid       int       `json:"pid"`
	Restarts  int       `json:"restarts"`
	UptimeMs  int64     `json:"uptime_ms"`
	StartedAt time.Time `json:"started_at,omitempty"`
	LastExit  string    `json:"last_exit,omitempty"`
	LastError string    `json:"last_error,omitempty"`
	Cwd       string    `json:"cwd"`
	Command   []string  `json:"command,omitempty"`
	OutLog    string    `json:"out_log"`
	ErrLog    string    `json:"err_log"`
	Spec      Spec      `json:"spec"`
}

// RPC params.
type SelectParams struct {
	Selector string `json:"selector"`
	Update   bool   `json:"update,omitempty"`
	// BaseEnv, when non-nil, replaces the app's inherited environment (restart --update-env).
	BaseEnv map[string]string `json:"base_env,omitempty"`
}

type PingResult struct {
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
	Pid      int    `json:"pid"`
	Apps     int    `json:"apps"`
}
