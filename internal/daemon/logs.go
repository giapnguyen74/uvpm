package daemon

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/giapnguyen74/uvpm/internal/home"
)

const (
	logMaxSize = 10 << 20 // rotate at app (re)start above this size
	logKeep    = 5
)

func logPaths(a *App) (out, err string) {
	base := fmt.Sprintf("%s-%d", a.Spec.Name, a.ID)
	return filepath.Join(home.LogsDir(), base+"-out.log"), filepath.Join(home.LogsDir(), base+"-err.log")
}

// openLogs opens the app's log files for appending. Children write to them
// directly (no pipe through the daemon), so they survive a daemon restart.
func openLogs(a *App) (outf, errf *os.File, err error) {
	op, ep := logPaths(a)
	rotate(op)
	rotate(ep)
	if outf, err = os.OpenFile(op, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err != nil {
		return nil, nil, err
	}
	if errf, err = os.OpenFile(ep, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err != nil {
		outf.Close()
		return nil, nil, err
	}
	return outf, errf, nil
}

func rotate(path string) {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() < logMaxSize {
		return
	}
	os.Remove(fmt.Sprintf("%s.%d", path, logKeep))
	for i := logKeep - 1; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", path, i), fmt.Sprintf("%s.%d", path, i+1))
	}
	os.Rename(path, path+".1")
}
