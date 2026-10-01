package daemon

import (
	"context"
	"syscall"
	"time"
)

type exitInfo struct {
	Code   int
	Signal string
	Known  bool // false for adopted processes: their exit code is unreadable
}

func alive(pid int, ticks uint64) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil && err != syscall.EPERM {
		return false
	}
	if ticks != 0 {
		return startTicks(pid) == ticks
	}
	return true
}

// groupAlive reports whether any process of the app's process group is left.
func groupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || err == syscall.EPERM
}

// watchPid polls an adopted process until it disappears.
func watchPid(ctx context.Context, pid int, ticks uint64) <-chan exitInfo {
	ch := make(chan exitInfo, 1)
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !alive(pid, ticks) {
					ch <- exitInfo{}
					return
				}
			}
		}
	}()
	return ch
}

// terminate stops the app's process group: SIGTERM, then SIGKILL after timeout.
func terminate(pgid int, timeout time.Duration) {
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	deadline := time.Now().Add(timeout)
	for groupAlive(pgid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if groupAlive(pgid) {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		for i := 0; i < 60 && groupAlive(pgid); i++ {
			time.Sleep(50 * time.Millisecond)
		}
	}
}
