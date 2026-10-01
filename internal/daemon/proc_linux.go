//go:build linux

package daemon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// startTicks returns the process start time (field 22 of /proc/<pid>/stat),
// which together with the pid identifies a process across pid reuse.
func startTicks(pid int) uint64 {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return 0
	}
	v, _ := strconv.ParseUint(f[19], 10, 64)
	return v
}
