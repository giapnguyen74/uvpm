//go:build !linux && !darwin

package daemon

// startTicks is unavailable here; without it processes are never adopted.
func startTicks(pid int) uint64 { return 0 }
