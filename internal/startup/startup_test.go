package startup

import (
	"strings"
	"testing"
)

func TestUnit(t *testing.T) {
	u := Unit("/home/u/bin/uvpm", "/data/uvpm")
	for _, want := range []string{"ExecStart=/home/u/bin/uvpm daemon\n", "KillMode=process", "UVPM_HOME=/data/uvpm", "WantedBy=default.target"} {
		if !strings.Contains(u, want) {
			t.Errorf("unit missing %q:\n%s", want, u)
		}
	}
	if strings.Contains(Unit("/x", ""), "UVPM_HOME") {
		t.Error("UVPM_HOME should be omitted when unset")
	}
}

func TestPlist(t *testing.T) {
	p := Plist("/opt/a&b/uvpm", "/data/uvpm", "/data/uvpm/daemon.log")
	for _, want := range []string{"<string>com.uvpm.daemon</string>", "/opt/a&amp;b/uvpm", "<string>daemon</string>", "RunAtLoad", "AbandonProcessGroup", "UVPM_HOME"} {
		if !strings.Contains(p, want) {
			t.Errorf("plist missing %q", want)
		}
	}
}
