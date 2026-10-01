# uvpm — TODO

Not implemented yet. See [plan.md](plan.md) for the design and the README for what exists today.

## Secrets and environment

uvpm injects environment into apps so the workload does not manage secrets itself. Today: shell snapshot (`base_env`) < `~/.uvpm/env` < app `--env-file` < `--env`. Plain text, protected by file mode 0600 only.

- [ ] **Stop storing the shell snapshot by default (or make it opt-in).** `state.json` currently holds every exported variable of the shell that ran `start`. Options: `--inherit-env` opt-in, or store only an allowlist/denylist (keep `PATH`, `HOME`, `LANG`, `XDG_*`, `DBUS_*`; drop `*_KEY`, `*_TOKEN`, `*_SECRET`, `*_PASSWORD`). Decide the default, then document it.
- [ ] **`uvpm env` commands**, scoped global (`~/.uvpm/env`) or per app (`--app <name>`, backed by a per-app file in `~/.uvpm/env.d/<name>`):
  - [ ] `uvpm env set KEY=VALUE [--app name]` (create file with mode 0600, preserve comments, atomic write)
  - [ ] `uvpm env unset KEY [--app name]`
  - [ ] `uvpm env list [--app name]` — keys only, never values; `--show` to reveal after confirmation
  - [ ] hint `uvpm restart <app>` after a change (running apps do not see changes until restart)
- [ ] **Mask secret values in output.** `describe` prints explicit `--env` values today. Mask values whose key matches `*KEY*|*TOKEN*|*SECRET*|*PASSWORD*|*CREDENTIAL*` (show `****`), everywhere: `describe`, `list --json` (when added), daemon log, error messages.
- [ ] **Check file permissions.** Warn (or refuse) when `~/.uvpm/env`, `env.d/*`, `--env-file` targets, or `state.json` are group/world readable; fix with `chmod 600` hint. Also verify `~/.uvpm` itself is 0700.
- [ ] **Keep secrets off command lines.** Warn when `start`/`--env` carries a key that looks like a secret ("visible in `ps` and saved in state.json; use `uvpm env set`"); same for secret-looking values in `-- args`.
- [ ] **`restart --update-env` merge mode.** Today it replaces the whole snapshot; add `--update-env KEY[,KEY]` to refresh only named variables from the current shell.
- [ ] **Optional encrypted store / OS keychain backend** (macOS Keychain, Linux Secret Service or `systemd-creds`). Only if plain-text files are not acceptable; design first (how the daemon unlocks it unattended is the hard part).
- [ ] Tests: layering order, masking, permission warnings, `env set/unset` round-trip (comments and quoting preserved), snapshot allowlist.
- [ ] Document the threat model in README: same-user processes can read `/proc/<pid>/environ` (Linux) or `ps eww` (macOS); uvpm is not a defence against that.

## Config file (plan §5)

- [ ] `uvpm.config.yaml` (also `.json`/`.toml`): `apps:` list with the fields from `start`, `start <config>` and `restart <config>`.
- [ ] `~` and relative-path expansion (relative to the config file), `env_file`, per-app `kill_timeout`, `max_restarts`.
- [ ] `instances: N` (names `name-0..N-1`, shared spec).
- [ ] Validation with clear errors (unknown keys, duplicate names).
- [ ] Optional `build:` command run by `restart --update` before the restart (for compiled apps such as `any-code-web`: `make build`); abort the restart if it fails, like a failed `uv sync`.

## Supervision and runtime

- [ ] `cron_restart` (cron expression; one-shot jobs re-run on schedule).
- [ ] CPU / memory columns in `list` (Linux `/proc`, macOS `ps`/sysctl).
- [ ] `list --json` and `describe --json`.
- [ ] Exact exit codes for adopted processes are unknown today; revisit the per-app shim idea if this hurts.
- [ ] macOS: pid-reuse guard for adoption (start time via `ps -o lstart=`), so it matches Linux.
- [ ] Log rotation while an app runs (today: only at (re)start); consider copy-truncate.
- [ ] `uvpm logs --since/--json`, per-app `--lines` default.
- [ ] Optional `--healthcheck` (tcp/http/log line) feeding status and, later, `reload`.
- [ ] `reload` (start new, wait healthy, stop old) for apps that tolerate two instances.

## TTY workloads (plan §4.1)

- [ ] `tty: true` / `--tty`: pty apps, `uvpm attach`, detach keys, resize, scrollback buffer.
- [ ] `uvpm send <name> "text"` and `stdin: true` apps.
- [ ] Hidden `uvpm shim` per tty app so pty/log/exit code survive daemon restarts.
- [ ] `uvpm run <target> -- args` foreground one-shot (pty when stdin is a TTY, child's exit code).

## Daemon lifecycle and release

- [ ] `uvpm update`: install new binary, drain, restart, health check, rollback (plan §2.2).
- [ ] `uvpm monit` TUI.
- [ ] Shell completions; man page.
- [ ] goreleaser config, install script, checksums.
- [ ] CI: macOS job; systemd `--user` integration test (linger, enable/disable, resurrect after restart).
- [ ] Test `uvpm startup` / `shutdown` for real on Linux (systemd) and macOS (launchd); only the generated unit/plist are tested today.
- [ ] Decide whether `shutdown` should also stop the daemon and apps; today it only removes the login item.

## Housekeeping

- [ ] Integration tests with real uv fixtures (project, PEP 723 script, plain script) in `internal/runner`/`daemon`; the manual run on macOS passed, nothing automated yet.
- [ ] `go.mod` / CI Go version policy.
- [ ] Commit the initial implementation (nothing committed yet).
