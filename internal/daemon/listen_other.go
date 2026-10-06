//go:build !linux && !darwin

package daemon

// listeners is unavailable on this platform.
func listeners(pgid int) []string { return nil }
