//go:build darwin

package daemon

import (
	"hash/fnv"
	"os/exec"
	"strconv"
	"strings"
)

// startTicks identifies a process by its start time (`ps -o lstart=`), which
// together with the pid survives pid reuse after a reboot.
func startTicks(pid int) uint64 {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	s := strings.TrimSpace(string(out))
	if err != nil || s == "" {
		return 0
	}
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64() | 1 // never 0
}
