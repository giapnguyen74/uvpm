//go:build darwin

package daemon

import (
	"os/exec"
	"strconv"
	"strings"
)

// listeners returns the TCP addresses ("*:8000", "127.0.0.1:8000") that any
// process in the process group pgid is listening on.
func listeners(pgid int) []string {
	bin, err := exec.LookPath("lsof")
	if err != nil {
		bin = "/usr/sbin/lsof" // not on the default PATH
	}
	out, _ := exec.Command(bin, "-nP", "-a", "-g", strconv.Itoa(pgid), "-iTCP", "-sTCP:LISTEN", "-Fn").Output()
	var res []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "n") {
			res = append(res, l[1:])
		}
	}
	return res
}
