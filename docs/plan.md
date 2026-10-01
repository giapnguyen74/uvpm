# uvpm — Project Plan

`uvpm` is a process manager written in Go, inspired by [PM2](https://pm2.keymetrics.io/). PM2 targets Node.js; `uvpm` targets **Python projects managed by [uv](https://docs.astral.sh/uv/)** and also runs **raw scripts/commands** (shell, Python, Node, binaries, anything executable).

> **Implementation status:** M0–M2 and the core of M1/M3/M4 are implemented (see README). Remaining: config file, `instances`, `cron_restart`, metrics, tty/shim, `uvpm update`, `reload`, `monit`.

## 1. Goals

- Keep long-running apps alive: start, stop, restart, auto-restart on crash, survive reboot.
- First-class `uv` support: detect `pyproject.toml` / `uv.lock`, sync the environment, run via `uv run`.
- Raw execution: run any script or command without needing a project.
- Single static Go binary, no runtime dependency except `uv` (only for uv apps).
- Familiar PM2-like UX (`start`, `stop`, `restart`, `delete`, `list`, `logs`, `save`, `startup`).
- Declarative config file for multi-app setups.

### Non-goals (v1)

- Cluster mode / load balancing across instances (PM2 cluster). Multi-instance via plain `instances: N` only.
- Remote deployment (`pm2 deploy`), SaaS dashboard.
- Windows: not supported, now or planned. Target platform is Linux; macOS builds are best-effort. `uvpm startup` on macOS installs a launchd agent only after showing the user what it will change and getting explicit approval (`--yes` to skip).
- Managing Python versions/installs itself — delegated to `uv`.

## 2. Architecture

Daemon + CLI client, like PM2.

```
 uvpm CLI  ──(unix socket, JSON-RPC)──►  uvpmd (daemon)
                                            │
                          ┌─────────────────┼──────────────────┐
                          ▼                 ▼                  ▼
                    Supervisor         Log manager        State store
                  (per-app goroutine)  (rotate, tail)    (state.json / bbolt)
                          │
                          ▼
                  os/exec child process (own process group)
```

- **CLI** (`uvpm`): parses commands, auto-spawns the daemon if absent, talks to it over a Unix socket at `~/.uvpm/uvpm.sock`.
- **Daemon** (`uvpm daemon`): owns all child processes; survives CLI exit (double-fork / `setsid`).
- **Supervisor**: one goroutine per app instance; handles spawn, wait, backoff restart, signal forwarding, graceful stop (SIGTERM → timeout → SIGKILL on process group).
- **Runner resolvers**: translate an app spec into a concrete command (see §4).
- **Log manager**: captures stdout/stderr to files, rotation, `logs` streaming/follow.
- **State store**: persists app specs and desired state so `resurrect`/startup can restore them.

Home dir layout:

```
~/.uvpm/
  uvpm.sock        daemon socket
  uvpm.pid
  state.json       saved process list (dump)
  logs/<name>-<id>-out.log / -err.log
  daemon.log
```

### 2.1 Running under systemd (`--user`)

The daemon is meant to run as a **systemd user service**, not as root and not as a system unit.

- `uvpm startup` writes `~/.config/systemd/user/uvpmd.service`, then runs `systemctl --user daemon-reload` and `systemctl --user enable --now uvpmd`.
- Unit sketch:

  ```ini
  [Unit]
  Description=uvpm daemon
  After=network.target

  [Service]
  Type=simple
  ExecStart=/path/to/uvpm daemon
  Restart=on-failure
  KillMode=process        # systemd stops only the daemon; the daemon stops its own children gracefully
  TimeoutStopSec=30
  Environment=PATH=%h/.local/bin:/usr/local/bin:/usr/bin:/bin

  [Install]
  WantedBy=default.target
  ```

- Boot without login: `startup` checks `loginctl show-user $USER -p Linger` and, if linger is off, tells the user to run `loginctl enable-linger $USER` (needs privileges; uvpm does not sudo).
- `uvpm daemon` always runs in the foreground (no self-daemonizing); under systemd its log goes to the journal. Outside systemd, the CLI auto-spawns a detached `uvpm daemon` (own session) with output in `~/.uvpm/daemon.log`. A lock file (`~/.uvpm/uvpm.lock`, flock) guarantees a single daemon. SIGTERM/SIGINT drain; only `uvpm kill` stops apps.
- Work folder is always `~/.uvpm` (override with `UVPM_HOME`, mainly for tests); socket, pid, state and logs all live there. No XDG paths. The unit file is the only thing outside it.
- The daemon's environment is minimal under systemd, so `uv` must be found: resolve `uv` from PATH, then `~/.local/bin/uv`, `~/.cargo/bin/uv`; the unit sets PATH explicitly, and `uvpm startup` captures the current `uv` location.
- `KillMode=process` vs `control-group` is an open decision (see §9): `control-group` is simpler and avoids orphans, but kills apps abruptly on daemon stop/upgrade.

### 2.2 Upgrading the daemon

Goal: replace the daemon binary and restart the daemon **without restarting managed apps**.

**Prerequisite: apps must not depend on the daemon staying alive.**

- Each app is started in its own session/process group (`setsid`), and the unit uses `KillMode=process`, so stopping the daemon leaves apps running (decision for §9: use `process`).
- Child stdout/stderr go **directly to log files** (fds opened by the daemon and passed to the child), not through a pipe the daemon pumps. A daemon restart then cannot break logging or send SIGPIPE to the app. Rotation is done by the daemon (rename + signal-free reopen is impossible for a running child, so use copy-truncate or rotate at app start).
- Persist per app in `state.json`: pid, process start time (`/proc/<pid>/stat` field 22, guards against pid reuse), spec, desired state, restart count.

**Adoption on daemon start.** The new daemon loads `state.json`; for each app marked running it checks that the pid exists and its start time matches, and then adopts it: it watches the process by polling every 500ms (`pidfd_open` is a possible later optimisation). The daemon is no longer the parent, so it cannot read the exit code of an adopted process; it records the exit as "unknown" and applies the normal restart policy. Apps whose pid is gone are treated as crashed while the daemon was down and follow the restart policy.

**Upgrade flow** (`uvpm update`, or manually replace the binary then `uvpm daemon restart`):

1. Install the new binary to `~/.uvpm/bin/uvpm-<version>` and atomically repoint the `~/.uvpm/bin/uvpm` symlink (the unit's `ExecStart` uses this stable path; Linux lets you replace a running binary).
2. CLI asks the old daemon to `drain`: write `state.json`, stop accepting RPCs, exit **without** signalling apps.
3. `systemctl --user restart uvpmd` (or, outside systemd, the CLI spawns the new daemon).
4. New daemon adopts apps (above). On start the daemon itself restores `state.json`: running apps are adopted, apps that should be running but whose process is gone are started. `save`/`resurrect` use a separate `dump.json` snapshot.
5. CLI waits for a health check (`ping` returns the new version and the adopted app count matches). On failure it repoints the symlink to the previous version and restarts, i.e. automatic rollback.

**Versioning.**

- RPC handshake carries `protocol` and `version`. A CLI/daemon mismatch within the same protocol major works. If it is incompatible, the CLI prints "daemon is vX, run `uvpm daemon restart`" instead of failing obscurely.
- `state.json` has a `schema` number; the daemon migrates older schemas on load and writes a `state.json.bak` first.

**Alternative considered: per-app shim** (like containerd-shim/conmon): a tiny parent process per app that keeps the child's pipes and exit code and reconnects to the new daemon. It gives exact exit codes and log-pipe ownership but adds a second binary and process per app. Start with adoption (simpler); revisit the shim if unknown exit codes prove painful.

## 3. CLI surface

| Command | Description |
|---|---|
| `uvpm run --name n <target> [-- child options]` | Start an app (target = dir, script, or command) |
| `uvpm run uvpm.config.yaml` | Start apps from config |
| `uvpm stop/restart/delete <name\|id\|all>` | Lifecycle |
| `uvpm list` (`ls`, `status`) | Table: id, name, mode, status, restarts, uptime, cpu, mem |
| `uvpm describe <name>` | Full spec + runtime details |
| `uvpm attach <name>` / `uvpm send <name> "text"` | Attach terminal to a tty app / write to its stdin (§4.1) |
| `uvpm run <target> [-- args]` | One-shot foreground run, no daemon app (§4.1) |
| `uvpm logs [name] [--lines N] [--follow] [--err]` | View logs |
| `uvpm flush [name]` | Truncate logs |
| `uvpm save` / `uvpm resurrect` | Persist / restore process list |
| `uvpm startup` / `shutdown` | Install / remove the systemd **user** unit that runs the daemon (see §2.1) |
| `uvpm monit` | (later) TUI dashboard |
| `uvpm daemon` / `daemon restart` / `kill` | Run / restart (apps keep running) / stop the daemon |
| `uvpm update` | Install new version, restart daemon with adoption and rollback (§2.2) |

Flags on `start`: `--name`, `--cwd`, `--env K=V`, `--env-file`, `--instances`, `--tty`, `--stdin`, `--no-autorestart`, `--max-restarts`, `--restart-delay`, `--cron-restart`, `--kill-timeout`, `--mode`, `--python`, `--with`, `--group/--extra`.

## 4. Execution modes

`uvpm run <target>` resolves the mode automatically (override with `--mode`).

| Mode | Detection | Resulting command |
|---|---|---|
| **uv project** | `<target>` is a directory (or file inside one) with `pyproject.toml`; `uv` on PATH | `uv run [--frozen] <entry>` |
| **uv script** | `.py` file with PEP 723 inline metadata (`# /// script`) | `uv run --script <file> [args]` |
| **python script** | plain `.py`, no metadata | `uv run <file>` (or `python` if `--no-uv`) |
| **raw command** | anything else, or `--mode raw` / `-- cmd args` | exec as given (`.sh` → `sh`, executable bit → direct, else via shell with `--shell`) |

uv project specifics:

- Entry resolution order: explicit `--entry`/config `script`; `[project.scripts]` entry (prompt/error if ambiguous); `module:` via `python -m`.
- Env prep: optionally run `uv sync [--frozen] [--no-dev]` before first start and on `restart --update`.
- Honour `.python-version`, `UV_PROJECT_ENVIRONMENT`, `--python`, `--with`, `--group`, `--extra`.
- Load `.env` if present (`uv run --env-file` or daemon-side injection).
- Detect missing `uv` and print an actionable install hint.

### 4.1 CLI / TTY workloads

Not every workload is a headless server. uvpm supports two more kinds:

**Interactive TTY apps** (REPLs, TUIs, curses programs, chat/CLI bots, anything that needs a terminal or reads stdin):

- `tty: true` in config (or `--tty` on `start`) runs the app under a **pty** instead of pipes, so the program sees a real terminal (colours, line editing, `isatty()`, `breakpoint()`/pdb).
- `uvpm attach <name>` connects your terminal to the app's pty (like `tmux attach`/`docker attach`): raw mode, window-resize (SIGWINCH) forwarded, detach with `Ctrl-P Ctrl-Q` (configurable) without stopping the app. Multiple attached clients are allowed; the first is read-write, the rest read-only unless `--rw`.
- Output is tee'd to the normal log files, so `uvpm logs` works while nobody is attached. A small scrollback ring buffer (default 64KB) is replayed on attach.
- `uvpm send <name> "text"` writes to the app's stdin without attaching (also works for non-tty apps started with `stdin: true`; otherwise stdin is `/dev/null`).
- Default `TERM=xterm-256color`; size defaults to 80x24 until a client attaches.

**One-shot / batch jobs** (scripts that run to completion, scheduled tasks, migrations):

- `autorestart: false` means the process runs once and is kept in the list with status `exited`/`errored`, exit code, and duration (`uvpm list`, `describe`). `restart` runs it again.
- `cron_restart` re-runs it on a schedule (cron-style job); `uvpm run <target> [-- args]` is a foreground convenience that executes a script/command under the same resolvers (§4), streams output to the terminal with a pty if stdin is a TTY, and exits with the child's exit code, without registering a daemon app.

**Interaction with daemon upgrades (§2.2).** A pty master fd lives in the daemon; if the daemon exits, the app gets SIGHUP. So tty apps (and `stdin: true` apps) are started through a **per-app shim**: the hidden subcommand `uvpm shim` (same binary, no extra install) that owns the pty master, the log tee, the scrollback buffer, and the child's exit code, and exposes a small per-app socket in `~/.uvpm/shims/<id>.sock`. The daemon and `attach` clients talk to the shim; a restarted daemon simply reconnects to the sockets listed in `state.json`. Non-tty apps keep the simpler adoption path from §2.2. The shim is only used where it is needed, and gives exact exit codes for those apps.

## 5. Config file

`uvpm.config.yaml` (also accept `.json`/`.toml`):

```yaml
apps:
  - name: api
    mode: uv                 # uv | script | raw (auto if omitted)
    cwd: ./services/api
    script: uvicorn          # project script, or `module: app.main`
    args: ["app.main:app", "--port", "8000"]
    instances: 2
    env:
      APP_ENV: production
    env_file: .env
    uv:
      sync: true
      frozen: true
      groups: [prod]
      python: "3.12"
    autorestart: true
    max_restarts: 10
    restart_delay: 1s
    backoff: exponential
    kill_timeout: 5s
    log:
      out: ./logs/api.out.log
      err: ./logs/api.err.log
      max_size: 10MB
      max_files: 5
  - name: backup
    mode: raw
    command: ./backup.sh
    cron_restart: "0 3 * * *"
    autorestart: false
```

### 5.1 Updating code under a running app

Apps run **in place** from `cwd`: uvpm never copies or installs the project, so you keep editing the code and the files on disk are always the source of truth. To make a running app pick up new code, restart it:

```
uvpm restart api              # plain restart
uvpm restart api --update     # re-sync dependencies if needed, then restart
uvpm restart all --update
```

`--update` behaviour:

- The daemon stores a hash of `pyproject.toml` + `uv.lock` per app at the last sync. If it changed, `uv sync` runs before the restart (`--frozen` if the app's config sets it).
- If the sync fails, the restart is aborted, the old process keeps running, and the error is shown in `describe` and `logs`.
- Only one update per project directory at a time (lock), so two `restart --update` calls cannot run two `uv sync`s at once.
- Restart is stop (SIGTERM, then SIGKILL after `kill_timeout`) followed by start. Zero-downtime reload, file watching and hooks are out of scope for v1; deploy scripts or git hooks can simply call `uvpm restart <name> --update`.

## 6. Project layout (Go)

```
cmd/uvpm/main.go
internal/
  cli/          cobra commands
  daemon/       server, RPC handlers, lifecycle
  rpc/          protocol types, client
  supervisor/   spawn, restart policy, backoff, signals
  runner/       uv.go, script.go, raw.go, detect.go
  config/       parse + validate + defaults
  logs/         file writer, rotation, tail/follow
  state/        persistence
  metrics/      cpu/mem sampling (gopsutil)
  startup/      systemd --user unit generator
docs/
```

Dependencies (keep small): `spf13/cobra`, `fsnotify`, `shirou/gopsutil`, `robfig/cron`, `gopkg.in/yaml.v3`, `BurntSushi/toml`, `lumberjack` (or own rotation), table renderer.

## 7. Milestones

**M0 — Skeleton (≈2 days)**
- `go mod init`, repo layout, CI (lint, test, build), Makefile, goreleaser config.

**M1 — Daemon + raw processes (≈1 week)**
- Daemon, Unix socket RPC, auto-spawn from CLI.
- `start/stop/restart/delete/list` for raw commands.
- Process-group management, graceful kill, log capture directly to files, `logs`.
- Upgrade-safe design from day one: `setsid` children, pid + start-time in state, adoption on daemon start, `daemon restart`.
- Restart policy with exponential backoff and max restarts.

**M2 — uv support (≈1 week)**
- Mode detection, `uv` resolver (project, PEP 723 script, plain py).
- `uv sync` pre-step, env/env-file, `--python/--with/--group`.
- Integration tests with fixture projects (requires `uv` in CI).

**M3 — Config + persistence (≈1 week)**
- YAML config, validation, multi-app start, `instances`.
- `save` / `resurrect`, `describe`, `flush`, log rotation.

**M4 — Production features (≈1–2 weeks)**
- `restart --update` (§5.1): lock-hash tracking with auto `uv sync`, per-project update lock.
- `startup` (systemd `--user` unit, linger check, resurrect on boot), `cron_restart`.
- CPU/mem metrics in `list`.
- Env-specific overrides (`env_production`).
- Environment inheritance (done): `start` snapshots the CLI shell's env into the spec (`base_env`, stored in the 0600 `state.json`); layering is shell env < `~/.uvpm/env` (global, re-read on every start, so late-arriving credentials need only a file edit and a restart) < app `env_file` < `--env`; `restart --update-env` re-captures; `--no-inherit-env` opts out.
- TTY workloads (§4.1): pty apps, `attach`/`send`, scrollback, `uvpm shim`, one-shot jobs and `uvpm run`.

**M5 — Polish & release**
- `monit` TUI, shell completions, docs/README, goreleaser binaries (linux amd64/arm64), install script.

## 8. Testing strategy

- Unit: detection logic, config parsing, backoff, log rotation.
- Integration: spin up daemon in temp `UVPM_HOME`, start fixture apps (crashing, long-running, signal-ignoring, forking children), assert state transitions and cleanup of process groups.
- uv fixtures: project with `[project.scripts]`, PEP 723 script, plain script.
- CI: ubuntu, Go stable, uv latest. A systemd `--user` test (container/VM with a user session) covers `startup`, linger, and resurrect-after-restart.

## 9. Risks & open questions

- **TTY + upgrades**: pty apps need the shim (§4.1) or the daemon restart would hang them up; test attach across `daemon restart`, resize handling, and shim crash/cleanup.
- **Orphaned grandchildren**: `uv run` spawns the Python process; kill must target the whole process group, not just `uv`. Consider `uv run` exec behaviour and test it.
- **Daemon upgrade/compat**: see §2.2. Adopted processes have no readable exit code; direct-to-file logs complicate rotation. Needs integration tests: restart daemon while apps run, kill an app while daemon is down, pid reuse.
- **Signal semantics** through `uv` wrappers and shells (`sh -c`) — prefer direct exec, avoid shell unless asked.
- **State format stability**: version the `state.json` schema.
- **systemd user session**: minimal env/PATH, no session without linger; `KillMode` choice affects whether apps survive a daemon restart.
- Open: reuse `.env` handling from uv vs. daemon-side injection (affects `describe` visibility).
- Open: should `start` on a uv project run `uv sync` by default, or only with `--sync`? (Proposed default: sync once if `.venv` is missing.)
