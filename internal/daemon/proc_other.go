//go:build !linux

package daemon

// startTicks is only available on Linux; elsewhere adoption relies on the pid alone.
func startTicks(pid int) uint64 { return 0 }
