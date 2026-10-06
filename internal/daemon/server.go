// Package daemon is the long-running process that supervises apps.
package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/giapnguyen74/uvpm/internal/home"
	"github.com/giapnguyen74/uvpm/internal/model"
	"github.com/giapnguyen74/uvpm/internal/rpc"
	"github.com/giapnguyen74/uvpm/internal/version"
)

// ErrAlreadyRunning is returned by Run when another daemon holds the lock.
var ErrAlreadyRunning = errors.New("uvpm daemon is already running")

type server struct {
	m    *Manager
	quit chan string // "drain" or "kill"
}

// Run starts the daemon and blocks until it is told to exit. SIGTERM/SIGINT
// (what systemd sends) drain: apps keep running and the next daemon adopts them.
func Run() error {
	if err := home.Ensure(); err != nil {
		return err
	}
	lock, err := os.OpenFile(home.LockFile(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return ErrAlreadyRunning
	}
	_ = os.Remove(home.Sock()) // stale socket; we hold the lock
	// Create the socket owner-only from the start (no window before a chmod).
	old := syscall.Umask(0o077)
	ln, err := net.Listen("unix", home.Sock())
	syscall.Umask(old)
	if err != nil {
		return err
	}
	_ = os.Chmod(home.Sock(), 0o600)

	m := NewManager()
	if err := m.Load(); err != nil {
		ln.Close()
		return fmt.Errorf("loading state: %w", err)
	}
	s := &server{m: m, quit: make(chan string, 1)}
	go rpc.Serve(ln, s.handle)
	log.Printf("uvpm daemon %s started (pid %d, home %s)", version.Version, os.Getpid(), home.Dir())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	mode := "drain"
	select {
	case <-sig:
	case mode = <-s.quit:
		time.Sleep(100 * time.Millisecond) // let the reply reach the client
	}
	log.Printf("daemon exiting (%s)", mode)
	if mode == "kill" {
		m.StopAll()
	} else {
		m.Drain()
	}
	ln.Close()
	_ = os.Remove(home.Sock())
	return nil
}

func (s *server) handle(method string, params json.RawMessage) (any, error) {
	switch method {
	case "ping":
		return model.PingResult{Version: version.Version, Protocol: version.Protocol, Pid: os.Getpid(), Apps: len(s.m.all())}, nil
	case "start":
		var spec model.Spec
		if err := json.Unmarshal(params, &spec); err != nil {
			return nil, err
		}
		if _, err := s.m.Start(spec); err != nil {
			return nil, err
		}
		return s.m.List(), nil
	case "list":
		return s.m.List(), nil
	case "describe", "stop", "resume", "restart", "delete", "flush":
		var p model.SelectParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		apps, err := s.m.Select(p.Selector)
		if err != nil {
			return nil, err
		}
		return s.apply(method, apps, p.Update, p.BaseEnv)
	case "save":
		if err := s.m.Dump(); err != nil {
			return nil, err
		}
		return nil, nil
	case "resurrect":
		n, err := s.m.Resurrect()
		if err != nil {
			return nil, err
		}
		return n, nil
	case "drain", "kill":
		select {
		case s.quit <- method:
		default:
		}
		return nil, nil
	}
	return nil, fmt.Errorf("unknown method %q", method)
}

func (s *server) apply(method string, apps []*App, update bool, env map[string]string) (any, error) {
	var errs []error
	for _, a := range apps {
		switch method {
		case "stop":
			s.m.Stop(a)
		case "resume":
			if err := s.m.Resume(a); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", a.Spec.Name, err))
			}
		case "restart":
			if err := s.m.Restart(a, update, env); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", a.Spec.Name, err))
			}
		case "delete":
			s.m.Delete(a)
		case "flush":
			s.m.Flush(a)
		case "describe":
			out := make([]model.AppInfo, 0, len(apps))
			for _, a := range apps {
				out = append(out, s.m.Info(a))
			}
			return out, nil
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return s.m.List(), nil
}
