package daemon

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/giapnguyen74/uvpm/internal/home"
	"github.com/giapnguyen74/uvpm/internal/model"
)

func setup(t *testing.T) *Manager {
	t.Helper()
	t.Setenv("UVPM_HOME", t.TempDir())
	if err := home.Ensure(); err != nil {
		t.Fatal(err)
	}
	return NewManager()
}

func rawSpec(name string, argv ...string) model.Spec {
	return model.Spec{Name: name, Target: argv[0], Args: argv[1:], Mode: "raw", Autorestart: true,
		MaxRestarts: 3, RestartDelayMs: 50, KillTimeoutMs: 1000}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func status(a *App) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Status
}

func TestStartStopRestartDelete(t *testing.T) {
	m := setup(t)
	a, err := m.Start(rawSpec("sleeper", "sleep", "60"))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "online", func() bool { return status(a) == model.StatusOnline })
	pid := a.Pid
	if !alive(pid, 0) {
		t.Fatal("process not running")
	}
	if _, err := m.Start(rawSpec("sleeper", "sleep", "60")); err == nil {
		t.Fatal("duplicate name accepted")
	}

	if err := m.Restart(a, false, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "online after restart", func() bool { return status(a) == model.StatusOnline && a.Pid != pid })
	waitFor(t, "old process gone", func() bool { return !alive(pid, 0) })

	m.Stop(a)
	if s := status(a); s != model.StatusStopped {
		t.Fatalf("status = %s", s)
	}
	if groupAlive(a.Pid) && a.Pid != 0 {
		t.Fatal("group still alive")
	}
	m.Delete(a)
	if len(m.List()) != 0 {
		t.Fatal("app not deleted")
	}
}

func TestCrashRestartsThenGivesUp(t *testing.T) {
	m := setup(t)
	a, _ := m.Start(rawSpec("crasher", "sh", "-c", "exit 3"))
	waitFor(t, "errored", func() bool { return status(a) == model.StatusErrored })
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Restarts < 3 {
		t.Fatalf("restarts = %d", a.Restarts)
	}
	if a.LastExit != "exited with code 3" {
		t.Fatalf("last exit = %q", a.LastExit)
	}
}

func TestOneShotExits(t *testing.T) {
	m := setup(t)
	spec := rawSpec("job", "sh", "-c", "echo hi")
	spec.Autorestart = false
	a, _ := m.Start(spec)
	waitFor(t, "exited", func() bool { return status(a) == model.StatusExited })
	op, _ := logPaths(a)
	b, _ := os.ReadFile(op)
	if string(b) != "hi\n" {
		t.Fatalf("log = %q", b)
	}
}

func TestGroupKilledOnStop(t *testing.T) {
	m := setup(t)
	pidfile := filepath.Join(t.TempDir(), "child.pid")
	a, _ := m.Start(rawSpec("tree", "sh", "-c", "sleep 60 & echo $! > "+pidfile+"; wait"))
	waitFor(t, "child pid", func() bool { _, err := os.Stat(pidfile); return err == nil })
	time.Sleep(100 * time.Millisecond)
	b, _ := os.ReadFile(pidfile)
	var child int
	for _, c := range string(b) {
		if c >= '0' && c <= '9' {
			child = child*10 + int(c-'0')
		}
	}
	m.Stop(a)
	waitFor(t, "grandchild gone", func() bool { return syscall.Kill(child, 0) != nil })
}

// A new daemon (Manager) adopts apps left running by the previous one.
func TestDrainAndAdopt(t *testing.T) {
	m1 := setup(t)
	a1, _ := m1.Start(rawSpec("keeper", "sleep", "60"))
	waitFor(t, "online", func() bool { return status(a1) == model.StatusOnline })
	pid := a1.Pid
	m1.Drain()
	if !alive(pid, 0) {
		t.Fatal("drain killed the app")
	}

	m2 := NewManager()
	if err := m2.Load(); err != nil {
		t.Fatal(err)
	}
	apps := m2.all()
	if len(apps) != 1 || apps[0].Pid != pid || status(apps[0]) != model.StatusOnline {
		t.Fatalf("not adopted: %+v", m2.List())
	}
	m2.Stop(apps[0])
	waitFor(t, "stopped", func() bool { return !alive(pid, 0) })
}

func TestAdoptRestartsDeadApp(t *testing.T) {
	m1 := setup(t)
	a1, _ := m1.Start(rawSpec("phoenix", "sleep", "60"))
	waitFor(t, "online", func() bool { return status(a1) == model.StatusOnline })
	pid := a1.Pid
	m1.Drain()
	syscall.Kill(-pid, syscall.SIGKILL) // dies while no daemon is watching
	waitFor(t, "dead", func() bool { return !alive(pid, 0) })

	m2 := NewManager()
	m2.Load()
	a2 := m2.all()[0]
	waitFor(t, "restarted", func() bool { return status(a2) == model.StatusOnline && a2.Pid != pid })
	m2.Stop(a2)
}

func TestEnvFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(f, []byte("# c\nA=1\nexport B=\"two words\"\nC='x' \nD=v # trailing\n"), 0o600)
	m, err := parseEnvFile(f)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "1", "B": "two words", "C": "x", "D": "v"}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %q, want %q", k, m[k], v)
		}
	}
}

func TestInheritedEnv(t *testing.T) {
	m := setup(t)
	out := filepath.Join(t.TempDir(), "out")
	spec := rawSpec("envy", "sh", "-c", "echo \"$FROM_SHELL/$EXPLICIT/$HOME_X\" > "+out)
	spec.Autorestart = false
	spec.BaseEnv = map[string]string{"FROM_SHELL": "a", "EXPLICIT": "old", "PATH": os.Getenv("PATH")}
	spec.Env = map[string]string{"EXPLICIT": "b"}
	a, _ := m.Start(spec)
	waitFor(t, "exit", func() bool { return status(a) == model.StatusExited })
	b, _ := os.ReadFile(out)
	if string(b) != "a/b/\n" {
		t.Fatalf("env = %q (daemon env must not leak in; explicit --env must win)", b)
	}
	// restart --update-env swaps the inherited layer
	m.Restart(a, false, map[string]string{"FROM_SHELL": "z", "PATH": os.Getenv("PATH")})
	waitFor(t, "exit again", func() bool { return status(a) == model.StatusExited })
	b, _ = os.ReadFile(out)
	if string(b) != "z/b/\n" {
		t.Fatalf("after update-env = %q", b)
	}
}

func TestGlobalEnvFileLayering(t *testing.T) {
	m := setup(t)
	os.WriteFile(home.EnvFile(), []byte("KEY=global\nONLY_GLOBAL=g\n"), 0o600)
	out := filepath.Join(t.TempDir(), "out")
	spec := rawSpec("g", "sh", "-c", "echo \"$KEY/$ONLY_GLOBAL\" > "+out)
	spec.Autorestart = false
	spec.BaseEnv = map[string]string{"KEY": "shell", "PATH": os.Getenv("PATH")}
	a, _ := m.Start(spec)
	waitFor(t, "exit", func() bool { return status(a) == model.StatusExited })
	if b, _ := os.ReadFile(out); string(b) != "global/g\n" {
		t.Fatalf("got %q: global env file must override the shell snapshot", b)
	}
	// edited later: picked up on the next restart, no re-capture needed
	os.WriteFile(home.EnvFile(), []byte("KEY=later\nONLY_GLOBAL=g\n"), 0o600)
	m.Restart(a, false, nil)
	waitFor(t, "exit again", func() bool { return status(a) == model.StatusExited })
	if b, _ := os.ReadFile(out); string(b) != "later/g\n" {
		t.Fatalf("got %q", b)
	}
}
