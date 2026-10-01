// Package cli implements the uvpm command line.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/giapnguyen74/uvpm/internal/daemon"
	"github.com/giapnguyen74/uvpm/internal/home"
	"github.com/giapnguyen74/uvpm/internal/model"
	"github.com/giapnguyen74/uvpm/internal/rpc"
	"github.com/giapnguyen74/uvpm/internal/startup"
	"github.com/giapnguyen74/uvpm/internal/version"
)

const usage = `uvpm - process manager for uv projects and scripts

Usage: uvpm <command> [args]

  run --name <name> <target> [options] [-- child options]   run a uv project dir, python script or raw command (alias: start)
  list | ls | status                    list apps
  describe <name|id>                    show an app in detail
  stop|restart|delete <name|id|all>     lifecycle (restart --update re-syncs uv deps first)
  logs [name|id|all] [-n N] [-f]        show logs (--out / --err to pick a stream)
  flush [name|id|all]                   truncate logs
  save | resurrect                      write / restore the process list (dump.json)
  startup | shutdown [--yes]           install / remove the login item (systemd --user on Linux, launchd on macOS, asks first)
  daemon [restart]                      run the daemon in the foreground / restart it (apps keep running)
  kill                                  stop all apps and the daemon
  version

Work folder: ~/.uvpm (override with UVPM_HOME).
Run 'uvpm run -h' for run options. Everything after '--' is passed to the child.
`

// Run executes the CLI and returns the process exit code.
func Run(args []string) int {
	if len(args) == 0 {
		fmt.Print(usage)
		return 0
	}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "run", "start":
		err = cmdStart(rest)
	case "list", "ls", "status":
		err = cmdList()
	case "describe", "show":
		err = cmdDescribe(rest)
	case "stop", "delete", "del", "rm":
		err = cmdSelect(map[string]string{"del": "delete", "rm": "delete"}[cmd], cmd, rest, false, nil)
	case "restart":
		err = cmdRestart(rest)
	case "flush":
		err = cmdSelect("flush", cmd, rest, false, nil)
	case "logs":
		err = cmdLogs(rest)
	case "save":
		err = call("save", nil, nil)
		if err == nil {
			fmt.Println("saved to", home.DumpFile())
		}
	case "resurrect":
		var n int
		if err = call("resurrect", nil, &n); err == nil {
			fmt.Printf("started %d app(s) from %s\n", n, home.DumpFile())
		}
	case "startup":
		err = cmdStartup(rest)
	case "shutdown":
		err = cmdShutdown(rest)
	case "daemon":
		err = cmdDaemon(rest)
	case "kill":
		err = cmdKill()
	case "version", "-v", "--version":
		fmt.Println("uvpm", version.Version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "uvpm: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(os.Stderr, "uvpm:", err)
		return 1
	}
	return 0
}

func cmdStart(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	var (
		name, cwd, mode, envFile, entry, module, python string
		envs, with, groups, extras                      strList
		noRestart, frozen, noUV, shell, noInherit       bool
		maxRestarts                                     = fs.Int("max-restarts", 15, "give up after N consecutive failed restarts (0 = unlimited)")
		restartDelay                                    = fs.Duration("restart-delay", time.Second, "base delay between restarts (doubles on repeated failures)")
		killTimeout                                     = fs.Duration("kill-timeout", 5*time.Second, "SIGTERM grace period before SIGKILL")
	)
	fs.StringVar(&name, "name", "", "app name (required)")
	fs.StringVar(&cwd, "cwd", "", "working directory (default: project dir, or the current dir for scripts)")
	fs.StringVar(&mode, "mode", "", "force mode: uv, script or raw (default: auto-detect)")
	fs.StringVar(&envFile, "env-file", "", "file with KEY=VALUE lines, read on every start")
	fs.StringVar(&entry, "script", "", "uv project: [project.scripts] entry to run")
	fs.StringVar(&module, "module", "", "uv project: run `python -m <module>`")
	fs.StringVar(&python, "python", "", "uv: python version")
	fs.Var(&envs, "env", "KEY=VALUE (repeatable)")
	fs.Var(&with, "with", "uv: extra requirement (repeatable)")
	fs.Var(&groups, "group", "uv project: dependency group to sync (repeatable)")
	fs.Var(&extras, "extra", "uv project: extra to sync (repeatable)")
	fs.BoolVar(&noRestart, "no-autorestart", false, "run once; do not restart when the process exits")
	fs.BoolVar(&frozen, "frozen", false, "uv project: sync with --frozen")
	fs.BoolVar(&noUV, "no-uv", false, "run .py files with plain python3 instead of uv")
	fs.BoolVar(&noInherit, "no-inherit-env", false, "do not capture this shell's environment (use the daemon's)")
	fs.BoolVar(&shell, "shell", false, "run the command through /bin/sh -c")
	pos, extra, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: uvpm run --name <name> [options] <target> [-- child options]")
	}
	target := pos[0]
	isDir := false
	if fi, err := os.Stat(target); err == nil {
		if abs, err := filepath.Abs(target); err == nil {
			target = abs
		}
		isDir = fi.IsDir()
	}
	if cwd == "" && isDir {
		cwd = target
	} else if cwd == "" {
		cwd, _ = os.Getwd()
	} else if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	if name == "" {
		return errors.New("--name is required: uvpm run --name <name> <target> [-- child options]")
	}
	if err := model.ValidName(name); err != nil {
		return err
	}
	env := map[string]string{}
	for _, kv := range envs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("--env %q: want KEY=VALUE", kv)
		}
		env[k] = v
	}
	if envFile != "" {
		if envFile, err = filepath.Abs(envFile); err != nil {
			return err
		}
	}
	spec := model.Spec{
		Name: name, Target: target, Args: extra, Mode: mode, Cwd: cwd, Env: env, EnvFile: envFile,
		Entry: entry, Module: module, Python: python, With: with, Groups: groups, Extras: extras,
		Frozen: frozen, NoUV: noUV, Shell: shell,
		Autorestart: !noRestart, MaxRestarts: *maxRestarts,
		RestartDelayMs: int(restartDelay.Milliseconds()), KillTimeoutMs: int(killTimeout.Milliseconds()),
	}
	if !noInherit {
		spec.BaseEnv = captureEnv()
	}
	var apps []model.AppInfo
	if err := call("start", spec, &apps); err != nil {
		return err
	}
	printTable(os.Stdout, apps)
	return nil
}

func cmdList() error {
	var apps []model.AppInfo
	if err := call("list", nil, &apps); err != nil {
		return err
	}
	printTable(os.Stdout, apps)
	return nil
}

func oneSelector(cmd string, args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("usage: uvpm %s <name|id|all>", cmd)
	}
	return args[0], nil
}

func cmdDescribe(args []string) error {
	sel, err := oneSelector("describe", args)
	if err != nil {
		return err
	}
	var apps []model.AppInfo
	if err := call("describe", model.SelectParams{Selector: sel}, &apps); err != nil {
		return err
	}
	for i, a := range apps {
		if i > 0 {
			fmt.Println()
		}
		printDescribe(os.Stdout, a)
	}
	return nil
}

// captureEnv snapshots this shell's environment for the app, minus values that
// only make sense for the current shell session.
func captureEnv() map[string]string {
	skip := map[string]bool{"PWD": true, "OLDPWD": true, "SHLVL": true, "_": true}
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && !skip[k] && !strings.HasPrefix(k, "UVPM_APP_") {
			env[k] = v
		}
	}
	return env
}

func cmdSelect(method, cmd string, args []string, update bool, env map[string]string) error {
	if method == "" {
		method = cmd
	}
	sel, err := oneSelector(cmd, args)
	if err != nil {
		return err
	}
	var apps []model.AppInfo
	if err := call(method, model.SelectParams{Selector: sel, Update: update, BaseEnv: env}, &apps); err != nil {
		return err
	}
	printTable(os.Stdout, apps)
	return nil
}

func cmdRestart(args []string) error {
	fs := flag.NewFlagSet("restart", flag.ContinueOnError)
	update := fs.Bool("update", false, "re-sync uv dependencies first when pyproject.toml/uv.lock changed")
	updateEnv := fs.Bool("update-env", false, "re-capture this shell's environment for the app")
	pos, _, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	var env map[string]string
	if *updateEnv {
		env = captureEnv()
	}
	return cmdSelect("restart", "restart", pos, *update, env)
}

func cmdLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	lines := fs.Int("n", 15, "lines to show per stream")
	follow := fs.Bool("f", false, "follow")
	onlyOut := fs.Bool("out", false, "stdout only")
	onlyErr := fs.Bool("err", false, "stderr only")
	pos, _, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	sel := "all"
	if len(pos) > 0 {
		sel = pos[0]
	}
	var apps []model.AppInfo
	if err := call("describe", model.SelectParams{Selector: sel}, &apps); err != nil {
		return err
	}
	showLogs(apps, *lines, *follow, *onlyOut, *onlyErr)
	return nil
}

// confirm shows what is about to happen and asks the user to approve it.
// Without a terminal, approval has to be given explicitly with --yes.
func confirm(title string, steps []string, yes bool) error {
	if !startup.NeedsApproval() || yes {
		return nil
	}
	fmt.Println(title)
	for _, s := range steps {
		fmt.Println("  -", s)
	}
	if fi, err := os.Stdin.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return errors.New("approval needed: re-run in a terminal, or pass --yes after reviewing the steps above")
	}
	fmt.Print("Proceed? [y/N] ")
	var ans string
	fmt.Scanln(&ans)
	if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
		return errors.New("cancelled; nothing was changed")
	}
	return nil
}

func yesFlag(name string, args []string) (bool, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	yes := fs.Bool("yes", false, "skip the confirmation prompt (macOS)")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	_, _, err := parseInterspersed(fs, args)
	return *yes, err
}

func cmdShutdown(args []string) error {
	yes, err := yesFlag("shutdown", args)
	if err != nil {
		return err
	}
	if err := confirm("uvpm will remove the login item for the uvpm daemon:",
		[]string{"unload and delete ~/Library/LaunchAgents/com.uvpm.daemon.plist", "running apps are not stopped"}, yes); err != nil {
		return err
	}
	return startup.Uninstall()
}

func cmdStartup(args []string) error {
	yes, err := yesFlag("startup", args)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	if err := confirm("uvpm startup will change your system. It will:",
		startup.Describe(exe, os.Getenv("UVPM_HOME")), yes); err != nil {
		return err
	}
	// Hand any auto-spawned daemon over to systemd; its apps get adopted.
	if _, err := ping(); err == nil {
		if err := rpc.Call(home.Sock(), "drain", nil, nil); err != nil {
			return err
		}
		if err := waitGone(); err != nil {
			return err
		}
	}
	advice, err := startup.Install(exe, os.Getenv("UVPM_HOME"))
	for _, l := range advice {
		fmt.Println(l)
	}
	return err
}

func cmdDaemon(args []string) error {
	if len(args) > 0 && args[0] == "restart" {
		return daemonRestart()
	}
	f, err := os.OpenFile(home.DaemonLog(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		f.Close()
	}
	err = daemon.Run()
	if errors.Is(err, daemon.ErrAlreadyRunning) {
		fmt.Fprintln(os.Stderr, "uvpm:", err)
		return nil
	}
	return err
}

// daemonRestart replaces the daemon process; managed apps keep running.
func daemonRestart() error {
	if startup.Active() {
		if err := startup.Restart(); err != nil {
			return err
		}
	} else {
		if _, err := ping(); err == nil {
			if err := rpc.Call(home.Sock(), "drain", nil, nil); err != nil {
				return err
			}
			if err := waitGone(); err != nil {
				return err
			}
		}
	}
	if err := ensureDaemon(); err != nil {
		return err
	}
	p, err := ping()
	if err != nil {
		return err
	}
	fmt.Printf("daemon %s running (pid %d), %d app(s)\n", p.Version, p.Pid, p.Apps)
	return nil
}

func cmdKill() error {
	if _, err := ping(); err != nil {
		fmt.Println("daemon is not running")
		return nil
	}
	if err := rpc.Call(home.Sock(), "kill", nil, nil); err != nil {
		return err
	}
	if err := waitGone(); err != nil {
		return err
	}
	fmt.Println("daemon and all apps stopped")
	return nil
}
