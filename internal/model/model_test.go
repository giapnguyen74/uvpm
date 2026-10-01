package model

import "testing"

func TestValidName(t *testing.T) {
	for _, ok := range []string{"app", "my-app_1", "a.b", "0x"} {
		if err := ValidName(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "../x", "a/b", ".hidden", "-x", "a b", "a\x00b"} {
		if ValidName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
