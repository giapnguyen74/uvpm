//go:build darwin

package daemon

import (
	"os"
	"testing"
)

func TestStartTicksDarwin(t *testing.T) {
	a, b := startTicks(os.Getpid()), startTicks(os.Getppid())
	if a == 0 || b == 0 || a == b {
		t.Fatalf("ticks self=%d parent=%d", a, b)
	}
	if a != startTicks(os.Getpid()) {
		t.Fatal("not stable")
	}
}
