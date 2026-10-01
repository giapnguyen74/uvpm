package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/giapnguyen74/uvpm/internal/model"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDetect(t *testing.T) {
	d := t.TempDir()
	py := write(t, d, "a.py", "print(1)")
	sh := write(t, d, "a.sh", "echo")
	cases := []struct {
		spec model.Spec
		want string
	}{
		{model.Spec{Target: d}, "uv"},
		{model.Spec{Target: py}, "script"},
		{model.Spec{Target: sh}, "raw"},
		{model.Spec{Target: "sleep"}, "raw"},
		{model.Spec{Target: d, Shell: true}, "raw"},
	}
	for _, c := range cases {
		if got := Detect(c.spec); got != c.want {
			t.Errorf("Detect(%+v) = %s, want %s", c.spec, got, c.want)
		}
	}
}

func TestProjectScripts(t *testing.T) {
	d := t.TempDir()
	write(t, d, "pyproject.toml", "[project]\nname='x'\n[project.scripts]\nb = \"x:b\"\n# c\n\"a\" = \"x:a\"\n[tool.x]\nz = 1\n")
	if got := ProjectScripts(d); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
}

func TestResolveProject(t *testing.T) {
	if _, err := FindUV(); err != nil {
		t.Skip("uv not installed")
	}
	d := t.TempDir()
	if _, err := Resolve(model.Spec{Target: d, Mode: "uv"}); err == nil {
		t.Fatal("expected error without pyproject.toml")
	}
	write(t, d, "pyproject.toml", "[project]\nname='x'\n[project.scripts]\nserve = \"x:s\"\n")
	r, err := Resolve(model.Spec{Target: d, Args: []string{"--port", "1"}, With: []string{"six"}})
	if err != nil {
		t.Fatal(err)
	}
	tail := strings.Join(r.Argv[1:], " ")
	want := "run --no-sync --project " + d + " --with six serve --port 1"
	if tail != want || r.Project != d || r.Dir != d {
		t.Fatalf("argv = %v project=%s dir=%s", r.Argv, r.Project, r.Dir)
	}
	r, _ = Resolve(model.Spec{Target: d, Module: "x.main"})
	if !strings.HasSuffix(strings.Join(r.Argv, " "), "python -m x.main") {
		t.Fatalf("argv = %v", r.Argv)
	}
}

func TestResolveScript(t *testing.T) {
	if _, err := FindUV(); err != nil {
		t.Skip("uv not installed")
	}
	d := t.TempDir()
	plain := write(t, d, "p.py", "print(1)")
	meta := write(t, d, "m.py", "# /// script\n# dependencies = []\n# ///\nprint(1)")
	r, _ := Resolve(model.Spec{Target: plain, Args: []string{"x"}})
	if strings.Contains(strings.Join(r.Argv, " "), "--script") || r.Argv[len(r.Argv)-1] != "x" {
		t.Fatalf("plain: %v", r.Argv)
	}
	r, _ = Resolve(model.Spec{Target: meta})
	if !strings.Contains(strings.Join(r.Argv, " "), "--script "+meta) || r.Dir != d {
		t.Fatalf("pep723: %v dir=%s", r.Argv, r.Dir)
	}
}

func TestResolveRaw(t *testing.T) {
	r, _ := Resolve(model.Spec{Target: "echo", Args: []string{"a", "b"}})
	if !reflect.DeepEqual(r.Argv, []string{"echo", "a", "b"}) {
		t.Fatalf("%v", r.Argv)
	}
	r, _ = Resolve(model.Spec{Target: "echo a | cat", Shell: true})
	if !reflect.DeepEqual(r.Argv, []string{"/bin/sh", "-c", "echo a | cat"}) {
		t.Fatalf("%v", r.Argv)
	}
}

func TestProjectHash(t *testing.T) {
	d := t.TempDir()
	write(t, d, "pyproject.toml", "a")
	h1 := ProjectHash(d)
	write(t, d, "uv.lock", "b")
	if h1 == ProjectHash(d) {
		t.Fatal("hash ignores uv.lock")
	}
}

func TestResolveMissingTargets(t *testing.T) {
	d := t.TempDir()
	cases := []struct {
		name string
		spec model.Spec
		want string
	}{
		{"script", model.Spec{Target: filepath.Join(d, "nope.py"), Mode: "script"}, "script not found"},
		{"script autodetect", model.Spec{Target: filepath.Join(d, "nope.py")}, "script not found"},
		{"project", model.Spec{Target: filepath.Join(d, "nodir"), Mode: "uv"}, "project directory not found"},
		{"raw path", model.Spec{Target: filepath.Join(d, "run.sh"), Mode: "raw"}, "command not found"},
		{"raw command", model.Spec{Target: "definitely-not-a-command-xyz", Mode: "raw"}, "command not found"},
		{"cwd", model.Spec{Target: "/bin/sh", Mode: "raw", Cwd: filepath.Join(d, "gone")}, "working directory not found"},
	}
	for _, c := range cases {
		_, err := Resolve(c.spec)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	if _, err := Resolve(model.Spec{Target: "sh", Mode: "raw"}); err != nil {
		t.Errorf("bare command on PATH rejected: %v", err)
	}
}
