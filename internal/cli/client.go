package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/giapnguyen74/uvpm/internal/home"
	"github.com/giapnguyen74/uvpm/internal/model"
	"github.com/giapnguyen74/uvpm/internal/rpc"
	"github.com/giapnguyen74/uvpm/internal/version"
)

func ping() (*model.PingResult, error) {
	var p model.PingResult
	if err := rpc.Call(home.Sock(), "ping", nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ensureDaemon returns once a daemon answers, spawning a detached one if needed.
func ensureDaemon() error {
	p, err := ping()
	var un *rpc.UnavailableError
	if errors.As(err, &un) {
		if err := spawnDaemon(); err != nil {
			return err
		}
		for i := 0; i < 100; i++ {
			if p, err = ping(); err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if err != nil {
		return fmt.Errorf("cannot reach the uvpm daemon: %w (see %s)", err, home.DaemonLog())
	}
	if p.Protocol != version.Protocol {
		return fmt.Errorf("daemon speaks protocol %d, this uvpm speaks %d: run `uvpm daemon restart`", p.Protocol, version.Protocol)
	}
	if p.Version != version.Version {
		fmt.Fprintf(os.Stderr, "note: daemon is %s, cli is %s; run `uvpm daemon restart` to upgrade it\n", p.Version, version.Version)
	}
	return nil
}

func spawnDaemon() error {
	if err := home.Ensure(); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(home.DaemonLog(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "daemon")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Dir = home.Dir()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// call talks to the daemon, starting it first when necessary.
func call(method string, params, result any) error {
	if err := ensureDaemon(); err != nil {
		return err
	}
	return rpc.Call(home.Sock(), method, params, result)
}

// waitGone waits until the daemon socket stops answering.
func waitGone() error {
	for i := 0; i < 200; i++ {
		if _, err := ping(); err != nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("daemon did not exit")
}
