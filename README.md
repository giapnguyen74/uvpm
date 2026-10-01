# uvpm

A PM2-style process manager written in Go, for [uv](https://docs.astral.sh/uv/) projects and raw scripts. Linux first (systemd `--user`); macOS is supported too, with an opt-in launchd login item. See [docs/plan.md](docs/plan.md) for the design and roadmap.

## Install

```
curl -fsSL https://raw.githubusercontent.com/giapnguyen74/uvpm/main/install.sh | sh
```

Installs the latest release to `~/.local/bin` (Linux/macOS, amd64/arm64). Set `UVPM_VERSION=v0.1.0` to pin a version or `UVPM_INSTALL_DIR` to change the location.

### Uninstall

```
uvpm shutdown          # remove the login item, if you ran `uvpm startup`
uvpm kill              # stop all apps and the daemon
rm ~/.local/bin/uvpm   # remove the binary (or $UVPM_INSTALL_DIR/uvpm)
rm -rf ~/.uvpm         # optional: delete state, logs and the saved process list
```

Run `uvpm shutdown` and `uvpm kill` before deleting the binary, since they need it.

## Usage

```
make build                      # bin/uvpm

uvpm run --name app ./myapp              # uv project (runs its single [project.scripts] entry)
uvpm run --name app ./myapp --script serve -- --port 8000
uvpm run --name app ./myapp --module app.main
uvpm run --name app job.py               # python script; PEP 723 inline metadata is honoured
uvpm run --name app ./backup.sh --no-autorestart
uvpm run --name once "sleep 5 && echo hi" --shell

uvpm ls | describe <app> | logs [app] [-f] | stop|restart|delete <app|all>
uvpm restart <app> --update     # uv sync first if pyproject.toml/uv.lock changed, then restart
uvpm startup                    # Linux: systemd --user unit (run `loginctl enable-linger $USER` to start at boot)
                                # macOS: launchd login item; shows exactly what it will change and asks first (--yes to skip)
uvpm daemon restart             # upgrade the daemon; running apps are adopted, not restarted
```

Apps start with the environment of the shell that ran `uvpm start` (like PM2), so no `--env-file` is needed; `--env K=V` and `--env-file` layer on top. The snapshot is stored in `~/.uvpm/state.json` (mode 0600) so restarts and reboots reproduce it. `uvpm restart <app> --update-env` re-captures the current shell's environment; `--no-inherit-env` opts out.

Credentials that appear later: CLIs that save a login to their own config or keychain (`claude`, `gh`, …) need nothing from uvpm, they find it through `HOME`. For API keys exported later, put them in `~/.uvpm/env` (`KEY=VALUE` lines, applied to every app, re-read on every start, overrides the shell snapshot) and `uvpm restart <app>`.

Everything lives in `~/.uvpm` (`UVPM_HOME` to override): socket, `state.json`, `dump.json`, `logs/`, `daemon.log`.

## Status

Implemented: daemon + CLI over a unix socket, raw/script/uv-project modes, restart policy with backoff, process-group kill, direct-to-file logs, state persistence with adoption across daemon restarts, `restart --update`, `save`/`resurrect`, systemd `--user` startup.

Not yet: config file, `instances`, `cron_restart`, cpu/mem in `list`, tty/`attach`, `uvpm update`, `reload`. See the milestones in the plan.
