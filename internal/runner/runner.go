// Package runner turns an app spec into a concrete command line.
package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/giapnguyen74/uvpm/internal/model"
)

// Result is a resolved command.
type Result struct {
	Mode    string
	Argv    []string
	Dir     string // working directory
	Project string // uv project dir, "" when the app is not a uv project
}

// Resolve picks the mode (when not explicit) and builds the command.
func Resolve(s model.Spec) (*Result, error) {
	mode := s.Mode
	if mode == "" {
		mode = Detect(s)
	}
	var (
		res *Result
		err error
	)
	switch mode {
	case "uv":
		res, err = resolveProject(s)
	case "script":
		res, err = resolveScript(s)
	case "raw":
		res, err = resolveRaw(s)
	default:
		return nil, fmt.Errorf("unknown mode %q (want uv, script or raw)", mode)
	}
	if err != nil {
		return nil, err
	}
	if res.Dir != "" {
		if fi, err := os.Stat(res.Dir); err != nil || !fi.IsDir() {
			return nil, fmt.Errorf("working directory not found: %s", res.Dir)
		}
	}
	return res, nil
}

// Detect guesses the mode from the target.
func Detect(s model.Spec) string {
	if s.Shell || !looksLikePath(s.Target) {
		return "raw"
	}
	fi, err := os.Stat(s.Target)
	if err != nil {
		if strings.HasSuffix(s.Target, ".py") {
			return "script" // missing file: resolveScript reports it
		}
		return "raw"
	}
	if fi.IsDir() {
		return "uv"
	}
	if strings.HasSuffix(s.Target, ".py") {
		return "script"
	}
	return "raw"
}

func looksLikePath(t string) bool { return strings.ContainsRune(t, '/') }

// FindUV locates the uv binary.
func FindUV() (string, error) {
	if p, err := exec.LookPath("uv"); err == nil {
		return p, nil
	}
	if h, err := os.UserHomeDir(); err == nil {
		for _, c := range []string{".local/bin/uv", ".cargo/bin/uv"} {
			p := filepath.Join(h, c)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p, nil
			}
		}
	}
	return "", errors.New("uv not found in PATH, ~/.local/bin or ~/.cargo/bin; install it: https://docs.astral.sh/uv/getting-started/installation/")
}

func resolveProject(s model.Spec) (*Result, error) {
	dir := s.Target
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("project directory not found: %s", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "pyproject.toml")); err != nil {
		return nil, fmt.Errorf("%s: no pyproject.toml (not a uv project)", dir)
	}
	uv, err := FindUV()
	if err != nil {
		return nil, err
	}
	// uvpm owns the sync step (see SyncCmd), so `uv run` never syncs by itself.
	argv := []string{uv, "run", "--no-sync", "--project", dir}
	for _, w := range s.With {
		argv = append(argv, "--with", w)
	}
	switch {
	case s.Module != "":
		argv = append(argv, "python", "-m", s.Module)
	case s.Entry != "":
		argv = append(argv, s.Entry)
	default:
		scripts := ProjectScripts(dir)
		switch len(scripts) {
		case 1:
			argv = append(argv, scripts[0])
		case 0:
			return nil, fmt.Errorf("%s: no [project.scripts] entry; use --script <name> or --module <module>", dir)
		default:
			return nil, fmt.Errorf("%s: several [project.scripts] entries (%s); pick one with --script", dir, strings.Join(scripts, ", "))
		}
	}
	argv = append(argv, s.Args...)
	return &Result{Mode: "uv", Argv: argv, Dir: orDefault(s.Cwd, dir), Project: dir}, nil
}

var pep723 = regexp.MustCompile(`(?m)^# /// script\s*$`)

// HasInlineMetadata reports whether a python file carries PEP 723 metadata.
func HasInlineMetadata(file string) bool {
	f, err := os.Open(file)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	return pep723.Match(buf[:n])
}

func resolveScript(s model.Spec) (*Result, error) {
	file := s.Target
	if fi, err := os.Stat(file); err != nil || fi.IsDir() {
		return nil, fmt.Errorf("script not found: %s", file)
	}
	dir := orDefault(s.Cwd, filepath.Dir(file))
	var argv []string
	if s.NoUV {
		py, err := exec.LookPath("python3")
		if err != nil {
			if py, err = exec.LookPath("python"); err != nil {
				return nil, errors.New("python not found in PATH")
			}
		}
		argv = []string{py, file}
	} else {
		uv, err := FindUV()
		if err != nil {
			return nil, err
		}
		argv = []string{uv, "run"}
		if s.Python != "" {
			argv = append(argv, "--python", s.Python)
		}
		for _, w := range s.With {
			argv = append(argv, "--with", w)
		}
		if HasInlineMetadata(file) {
			argv = append(argv, "--script")
		}
		argv = append(argv, file)
	}
	argv = append(argv, s.Args...)
	return &Result{Mode: "script", Argv: argv, Dir: dir}, nil
}

func resolveRaw(s model.Spec) (*Result, error) {
	if !s.Shell {
		if err := checkCommand(s); err != nil {
			return nil, err
		}
	}
	var argv []string
	switch {
	case s.Shell:
		argv = []string{"/bin/sh", "-c", strings.Join(append([]string{s.Target}, s.Args...), " ")}
	case strings.HasSuffix(s.Target, ".sh") && !isExecutable(s.Target):
		argv = append([]string{"/bin/sh", s.Target}, s.Args...)
	default:
		argv = append([]string{s.Target}, s.Args...)
	}
	return &Result{Mode: "raw", Argv: argv, Dir: s.Cwd}, nil
}

// checkCommand makes sure a non-shell target exists: a path must be a file
// (relative paths are resolved against the working directory, like exec does),
// a bare command must be on the app's PATH.
func checkCommand(s model.Spec) error {
	t := s.Target
	if strings.ContainsRune(t, '/') {
		p := t
		if !filepath.IsAbs(p) && s.Cwd != "" {
			p = filepath.Join(s.Cwd, p)
		}
		if fi, err := os.Stat(p); err != nil || fi.IsDir() {
			return fmt.Errorf("command not found: %s", t)
		}
		return nil
	}
	path := os.Getenv("PATH")
	for _, e := range []map[string]string{s.BaseEnv, s.Env} {
		if v, ok := e["PATH"]; ok {
			path = v
		}
	}
	for _, d := range filepath.SplitList(path) {
		if d == "" {
			d = "."
		}
		if isExecutable(filepath.Join(d, t)) {
			return nil
		}
	}
	return fmt.Errorf("command not found in PATH: %s", t)
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

func orDefault(v, d string) string {
	if v != "" {
		return v
	}
	return d
}

// ProjectScripts lists the names under [project.scripts] in pyproject.toml.
func ProjectScripts(dir string) []string {
	b, err := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if err != nil {
		return nil
	}
	var out []string
	in := false
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			in = line == "[project.scripts]"
			continue
		}
		if !in || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, _, ok := strings.Cut(line, "="); ok {
			out = append(out, strings.Trim(strings.TrimSpace(k), `"'`))
		}
	}
	sort.Strings(out)
	return out
}

// SyncCmd builds the `uv sync` command for a project.
func SyncCmd(s model.Spec, dir string) (*exec.Cmd, error) {
	uv, err := FindUV()
	if err != nil {
		return nil, err
	}
	args := []string{"sync"}
	if s.Frozen {
		args = append(args, "--frozen")
	}
	if s.Python != "" {
		args = append(args, "--python", s.Python)
	}
	for _, g := range s.Groups {
		args = append(args, "--group", g)
	}
	for _, e := range s.Extras {
		args = append(args, "--extra", e)
	}
	cmd := exec.Command(uv, args...)
	cmd.Dir = dir
	return cmd, nil
}

// ProjectHash fingerprints the files that decide the project's environment.
func ProjectHash(dir string) string {
	h := sha256.New()
	for _, f := range []string{"pyproject.toml", "uv.lock"} {
		b, _ := os.ReadFile(filepath.Join(dir, f))
		h.Write([]byte(f))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// VenvExists reports whether the project's virtualenv has been created.
func VenvExists(s model.Spec, dir string) bool {
	venv := ".venv"
	if v := s.Env["UV_PROJECT_ENVIRONMENT"]; v != "" {
		venv = v
	}
	if !filepath.IsAbs(venv) {
		venv = filepath.Join(dir, venv)
	}
	_, err := os.Stat(venv)
	return err == nil
}
