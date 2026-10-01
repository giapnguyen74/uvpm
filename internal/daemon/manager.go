package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/giapnguyen74/uvpm/internal/home"
	"github.com/giapnguyen74/uvpm/internal/model"
	"github.com/giapnguyen74/uvpm/internal/runner"
	"github.com/giapnguyen74/uvpm/internal/version"
)

const stableRun = 10 * time.Second // a run this long resets the restart backoff

type App struct {
	mu         sync.Mutex
	ID         int
	Spec       model.Spec
	Desired    string // running | stopped
	Status     string
	Pid        int
	StartTicks uint64
	StartedAt  time.Time
	Restarts   int
	LastExit   string
	LastError  string
	SyncHash   string

	cancel context.CancelFunc
	done   chan struct{}
}

func (a *App) set(f func()) {
	a.mu.Lock()
	f()
	a.mu.Unlock()
}

// Manager owns all apps and their supervisors.
type Manager struct {
	mu       sync.Mutex
	apps     map[int]*App
	nextID   int
	draining bool

	saveMu sync.Mutex
	projMu map[string]*sync.Mutex
}

func NewManager() *Manager {
	return &Manager{apps: map[int]*App{}, nextID: 1, projMu: map[string]*sync.Mutex{}}
}

func (m *Manager) projLock(dir string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.projMu[dir]
	if l == nil {
		l = &sync.Mutex{}
		m.projMu[dir] = l
	}
	return l
}

// ---- persistence -----------------------------------------------------------

func (m *Manager) snapshot() stateFile {
	m.mu.Lock()
	apps := m.sortedLocked()
	next := m.nextID
	m.mu.Unlock()
	st := stateFile{Schema: version.StateSchema, NextID: next}
	for _, a := range apps {
		a.mu.Lock()
		st.Apps = append(st.Apps, persistedApp{
			ID: a.ID, Spec: a.Spec, Desired: a.Desired, Status: a.Status, Pid: a.Pid,
			StartTicks: a.StartTicks, StartedAt: a.StartedAt, Restarts: a.Restarts,
			SyncHash: a.SyncHash, LastExit: a.LastExit,
		})
		a.mu.Unlock()
	}
	return st
}

func (m *Manager) save() {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	st := m.snapshot()
	if err := writeJSONAtomic(stateFilePath(), st); err != nil {
		log.Printf("saving state: %v", err)
	}
}

// Load restores apps from state.json: processes that are still running are
// adopted, apps that should be running but are gone are started again.
func (m *Manager) Load() error {
	st, err := readState(stateFilePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if b, err := os.ReadFile(stateFilePath()); err == nil {
		_ = os.WriteFile(stateFilePath()+".bak", b, 0o600)
	}
	m.mu.Lock()
	if st.NextID > m.nextID {
		m.nextID = st.NextID
	}
	m.mu.Unlock()
	for _, p := range st.Apps {
		if err := model.ValidName(p.Spec.Name); err != nil {
			log.Printf("skipping app %d from state: %v", p.ID, err)
			continue
		}
		a := &App{ID: p.ID, Spec: p.Spec, Desired: p.Desired, Status: p.Status, Pid: p.Pid,
			StartTicks: p.StartTicks, StartedAt: p.StartedAt, Restarts: p.Restarts,
			SyncHash: p.SyncHash, LastExit: p.LastExit}
		m.mu.Lock()
		m.apps[a.ID] = a
		if a.ID >= m.nextID {
			m.nextID = a.ID + 1
		}
		m.mu.Unlock()
		if a.Desired != "running" {
			continue
		}
		if adoptable(a.Pid, a.StartTicks) {
			log.Printf("adopting %s (pid %d)", a.Spec.Name, a.Pid)
			a.set(func() { a.Status = model.StatusOnline })
			m.launch(a, true)
		} else {
			log.Printf("starting %s (not running)", a.Spec.Name)
			if a.Pid > 0 {
				a.set(func() { a.LastExit = "gone while daemon was down" })
			}
			a.set(func() { a.Pid = 0; a.StartTicks = 0 })
			m.launch(a, false)
		}
	}
	m.save()
	return nil
}

// ---- lookup ---------------------------------------------------------------

func (m *Manager) sortedLocked() []*App {
	out := make([]*App, 0, len(m.apps))
	for _, a := range m.apps {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *Manager) all() []*App {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sortedLocked()
}

// Select resolves "all", an id, or a name.
func (m *Manager) Select(sel string) ([]*App, error) {
	apps := m.all()
	if sel == "all" {
		return apps, nil
	}
	id, numeric := strconv.Atoi(sel)
	for _, a := range apps {
		if (numeric == nil && a.ID == id) || a.Spec.Name == sel {
			return []*App{a}, nil
		}
	}
	return nil, fmt.Errorf("no app matches %q", sel)
}

func (m *Manager) Info(a *App) model.AppInfo {
	a.mu.Lock()
	info := model.AppInfo{
		ID: a.ID, Name: a.Spec.Name, Status: a.Status, Desired: a.Desired, Pid: a.Pid,
		Restarts: a.Restarts, StartedAt: a.StartedAt, LastExit: a.LastExit, LastError: a.LastError,
		Cwd: a.Spec.Cwd, Spec: a.Spec,
	}
	if a.Status == model.StatusOnline && !a.StartedAt.IsZero() {
		info.UptimeMs = time.Since(a.StartedAt).Milliseconds()
	}
	a.mu.Unlock()
	info.OutLog, info.ErrLog = logPaths(a)
	if res, err := runner.Resolve(info.Spec); err == nil {
		info.Mode, info.Command = res.Mode, res.Argv
		if info.Cwd == "" {
			info.Cwd = res.Dir
		}
	} else {
		info.Mode = info.Spec.Mode
		if info.Mode == "" {
			info.Mode = runner.Detect(info.Spec)
		}
	}
	return info
}

func (m *Manager) List() []model.AppInfo {
	apps := m.all()
	out := make([]model.AppInfo, 0, len(apps))
	for _, a := range apps {
		out = append(out, m.Info(a))
	}
	return out
}

// ---- commands -------------------------------------------------------------

func (m *Manager) Start(spec model.Spec) (*App, error) {
	if err := model.ValidName(spec.Name); err != nil {
		return nil, err
	}
	if _, err := runner.Resolve(spec); err != nil {
		return nil, err
	}
	m.mu.Lock()
	for _, a := range m.apps {
		if a.Spec.Name == spec.Name {
			m.mu.Unlock()
			return nil, fmt.Errorf("an app named %q already exists (id %d); delete it or use another --name", spec.Name, a.ID)
		}
	}
	a := &App{ID: m.nextID, Spec: spec, Desired: "running", Status: "starting"}
	m.nextID++
	m.apps[a.ID] = a
	m.mu.Unlock()
	m.launch(a, false)
	m.save()
	return a, nil
}

// Stop stops the app and waits until it is gone.
func (m *Manager) Stop(a *App) {
	m.stop(a)
	m.save()
}

func (m *Manager) stop(a *App) {
	a.mu.Lock()
	cancel, done := a.cancel, a.done
	a.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	a.set(func() {
		a.cancel, a.done = nil, nil
		a.Desired = "stopped"
		switch a.Status {
		case model.StatusOnline, model.StatusRestarting, model.StatusSyncing:
			a.Status = model.StatusStopped
			a.LastExit = "stopped by uvpm"
		}
		a.Pid, a.StartTicks = 0, 0
	})
}

// Restart restarts the app. With update, dependencies are synced first when
// pyproject.toml/uv.lock changed; a failed sync aborts and leaves the app as is.
func (m *Manager) Restart(a *App, update bool, newEnv map[string]string) error {
	if newEnv != nil {
		a.set(func() { a.Spec.BaseEnv = newEnv })
	}
	if update {
		if res, err := runner.Resolve(a.Spec); err == nil && res.Project != "" {
			a.mu.Lock()
			last := a.SyncHash
			a.mu.Unlock()
			if runner.ProjectHash(res.Project) != last || !runner.VenvExists(a.Spec, res.Project) {
				if err := m.syncProject(a, res.Project); err != nil {
					a.set(func() { a.LastError = err.Error() })
					m.save()
					return err
				}
			}
		}
	}
	// Fail fast when the target is gone instead of looping on a doomed start.
	if _, err := runner.Resolve(a.Spec); err != nil {
		m.stop(a)
		m.failStart(a, err)
		return fmt.Errorf("%s: failed to start: %w", a.Spec.Name, err)
	}
	m.stop(a)
	a.set(func() { a.Restarts++; a.LastError = "" })
	m.launch(a, false)
	m.save()
	return nil
}

// failStart parks the app as errored with a note: the start can never work
// until the user fixes the target, so it is not retried.
func (m *Manager) failStart(a *App, err error) {
	msg := "failed to start: " + err.Error()
	a.set(func() {
		a.Desired, a.Status, a.LastError = "stopped", model.StatusErrored, msg
		a.Pid, a.StartTicks = 0, 0
	})
	if _, errf, e := openLogs(a); e == nil {
		fmt.Fprintf(errf, "%s uvpm: %s\n", time.Now().Format(time.RFC3339), msg)
		errf.Close()
	}
	log.Printf("%s: %s", a.Spec.Name, msg)
	m.save()
}

func (m *Manager) Delete(a *App) {
	m.stop(a)
	m.mu.Lock()
	delete(m.apps, a.ID)
	m.mu.Unlock()
	m.save()
}

func (m *Manager) Flush(a *App) {
	op, ep := logPaths(a)
	_ = os.Truncate(op, 0)
	_ = os.Truncate(ep, 0)
}

// StopAll gracefully stops every app (used by `uvpm kill`).
func (m *Manager) StopAll() {
	var wg sync.WaitGroup
	for _, a := range m.all() {
		wg.Add(1)
		go func() { defer wg.Done(); m.stop(a) }()
	}
	wg.Wait()
	m.save()
}

// Drain detaches from all apps without touching them, for daemon upgrades.
func (m *Manager) Drain() {
	m.mu.Lock()
	m.draining = true
	m.mu.Unlock()
	m.save()
	apps := m.all()
	for _, a := range apps {
		a.mu.Lock()
		cancel := a.cancel
		a.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	for _, a := range apps {
		a.mu.Lock()
		done := a.done
		a.mu.Unlock()
		if done != nil {
			<-done
		}
	}
	m.save()
}

func (m *Manager) isDraining() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.draining
}

// ---- dump / resurrect -----------------------------------------------------

func (m *Manager) Dump() error {
	return writeJSONAtomic(home.DumpFile(), m.snapshot())
}

// Resurrect starts apps from dump.json that are not present yet.
func (m *Manager) Resurrect() (int, error) {
	st, err := readState(home.DumpFile())
	if err != nil {
		return 0, fmt.Errorf("nothing to resurrect (run `uvpm save` first): %w", err)
	}
	n := 0
	for _, p := range st.Apps {
		if p.Desired != "running" {
			continue
		}
		if _, err := m.Start(p.Spec); err == nil {
			n++
		}
	}
	return n, nil
}

// ---- supervision ----------------------------------------------------------

func (m *Manager) launch(a *App, adopt bool) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	a.set(func() {
		a.cancel, a.done, a.Desired = cancel, done, "running"
		if !adopt {
			a.Status = "starting"
		}
	})
	go func() {
		defer close(done)
		m.supervise(ctx, a, adopt)
	}()
}

func (m *Manager) supervise(ctx context.Context, a *App, adopt bool) {
	spec := a.Spec
	base := time.Duration(spec.RestartDelayMs) * time.Millisecond
	if base <= 0 {
		base = time.Second
	}
	killTimeout := time.Duration(spec.KillTimeoutMs) * time.Millisecond
	if killTimeout <= 0 {
		killTimeout = 5 * time.Second
	}
	consecutive := 0

	for {
		var (
			exited   <-chan exitInfo
			startErr error
			pid      int
			began    = time.Now()
		)
		if adopt {
			adopt = false
			a.mu.Lock()
			pid = a.Pid
			exited = watchPid(ctx, pid, a.StartTicks)
			if !a.StartedAt.IsZero() {
				began = a.StartedAt
			}
			a.mu.Unlock()
		} else {
			exited, pid, startErr = m.startOnce(ctx, a)
		}

		var ei exitInfo
		if startErr == nil {
			select {
			case <-ctx.Done():
				if !m.isDraining() {
					terminate(pid, killTimeout)
				}
				return
			case ei = <-exited:
				// The leader is gone; clean up anything it left in its group.
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
			a.set(func() { a.LastExit = describeExit(ei); a.Pid, a.StartTicks = 0, 0 })
		} else {
			if ctx.Err() != nil {
				return
			}
			var fe *fatalStartError
			if errors.As(startErr, &fe) {
				m.failStart(a, fe.err)
				return
			}
			log.Printf("%s: %v", spec.Name, startErr)
			a.set(func() { a.LastError = startErr.Error(); a.Status = model.StatusErrored })
		}

		if time.Since(began) >= stableRun {
			consecutive = 0
		}
		if !spec.Autorestart {
			a.set(func() {
				a.Desired = "stopped"
				if startErr != nil || (ei.Known && ei.Code != 0) || ei.Signal != "" {
					a.Status = model.StatusErrored
				} else {
					a.Status = model.StatusExited
				}
			})
			m.save()
			return
		}
		consecutive++
		if spec.MaxRestarts > 0 && consecutive > spec.MaxRestarts {
			a.set(func() {
				a.Desired, a.Status = "stopped", model.StatusErrored
				a.LastError = fmt.Sprintf("gave up after %d consecutive restarts", spec.MaxRestarts)
			})
			m.save()
			return
		}
		delay := base << min(consecutive-1, 5)
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
		a.set(func() { a.Status = model.StatusRestarting; a.Restarts++ })
		m.save()
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

func describeExit(ei exitInfo) string {
	switch {
	case ei.Signal != "":
		return "killed by " + ei.Signal
	case !ei.Known:
		return "exited (code unknown)"
	default:
		return fmt.Sprintf("exited with code %d", ei.Code)
	}
}

// fatalStartError is a start failure that retrying cannot fix (missing script,
// project, working directory or command).
type fatalStartError struct{ err error }

func (e *fatalStartError) Error() string { return e.err.Error() }
func (e *fatalStartError) Unwrap() error { return e.err }

// startOnce syncs (when needed) and spawns the process.
func (m *Manager) startOnce(ctx context.Context, a *App) (<-chan exitInfo, int, error) {
	res, err := runner.Resolve(a.Spec)
	if err != nil {
		return nil, 0, &fatalStartError{err}
	}
	if res.Project != "" {
		a.mu.Lock()
		last := a.SyncHash
		a.mu.Unlock()
		if last == "" || !runner.VenvExists(a.Spec, res.Project) {
			a.set(func() { a.Status = model.StatusSyncing })
			if err := m.syncProject(a, res.Project); err != nil {
				return nil, 0, err
			}
		}
	}
	if ctx.Err() != nil {
		return nil, 0, ctx.Err()
	}

	outf, errf, err := openLogs(a)
	if err != nil {
		return nil, 0, err
	}
	defer outf.Close()
	defer errf.Close()

	cmd := exec.Command(res.Argv[0], res.Argv[1:]...)
	cmd.Dir = res.Dir
	cmd.Env = buildEnv(a)
	cmd.Stdout, cmd.Stderr = outf, errf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // own session and process group
	if err := cmd.Start(); err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			return nil, 0, &fatalStartError{err}
		}
		return nil, 0, err
	}
	pid := cmd.Process.Pid
	ch := make(chan exitInfo, 1)
	go func() {
		err := cmd.Wait()
		ei := exitInfo{Known: true}
		if ps := cmd.ProcessState; ps != nil {
			ei.Code = ps.ExitCode()
			if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				ei.Signal = ws.Signal().String()
			}
		} else if err != nil {
			ei.Code = -1
		}
		ch <- ei
	}()
	a.set(func() {
		a.Pid, a.StartTicks, a.StartedAt = pid, startTicks(pid), time.Now()
		a.Status, a.LastError = model.StatusOnline, ""
	})
	m.save()
	return ch, pid, nil
}

// syncProject runs `uv sync`, one at a time per project directory.
func (m *Manager) syncProject(a *App, dir string) error {
	l := m.projLock(dir)
	l.Lock()
	defer l.Unlock()
	cmd, err := runner.SyncCmd(a.Spec, dir)
	if err != nil {
		return err
	}
	outf, errf, err := openLogs(a)
	if err != nil {
		return err
	}
	defer outf.Close()
	defer errf.Close()
	cmd.Env = buildEnv(a)
	cmd.Stdout, cmd.Stderr = outf, errf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("uv sync failed (%v); see `uvpm logs %s`", err, a.Spec.Name)
	}
	a.set(func() { a.SyncHash = runner.ProjectHash(dir) })
	return nil
}
