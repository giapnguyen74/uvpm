//go:build darwin

package daemon

import (
	"os"
	"testing"
)

func TestStartTicksDarwin(t *testing.T) {
	a := startTicks(os.Getpid())
	if a == 0 {
		t.Fatal("no start time for own pid")
	}
	if startTicks(1<<30) != 0 {
		t.Fatal("start time for a pid that does not exist")
	}
	if a != startTicks(os.Getpid()) {
		t.Fatal("not stable")
	}
}
