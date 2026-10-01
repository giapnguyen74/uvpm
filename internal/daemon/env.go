package daemon

import (
	"fmt"
	"os"
	"strings"

	"github.com/giapnguyen74/uvpm/internal/home"
)

// buildEnv is the app's inherited environment (the starting shell's, else the daemon's), then ~/.uvpm/env, then the app's --env-file, then explicit --env vars.
func buildEnv(a *App) []string {
	env := map[string]string{}
	if a.Spec.BaseEnv != nil {
		for k, v := range a.Spec.BaseEnv {
			env[k] = v
		}
	} else {
		for _, kv := range os.Environ() {
			if k, v, ok := strings.Cut(kv, "="); ok {
				env[k] = v
			}
		}
	}
	// ~/.uvpm/env applies to every app and is re-read on every (re)start, so
	// credentials that show up later only need a file edit and a restart.
	if m, err := parseEnvFile(home.EnvFile()); err == nil {
		for k, v := range m {
			env[k] = v
		}
	}
	if a.Spec.EnvFile != "" {
		if m, err := parseEnvFile(a.Spec.EnvFile); err == nil {
			for k, v := range m {
				env[k] = v
			}
		}
	}
	for k, v := range a.Spec.Env {
		env[k] = v
	}
	env["UVPM_APP_NAME"] = a.Spec.Name
	env["UVPM_APP_ID"] = fmt.Sprint(a.ID)
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// parseEnvFile reads KEY=VALUE lines (optional `export`, quotes and # comments).
func parseEnvFile(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		out[k] = v
	}
	return out, nil
}
