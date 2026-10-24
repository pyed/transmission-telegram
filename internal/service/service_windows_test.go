//go:build windows

package service

import "testing"

func TestConfigPathOf(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"-config", `C:\Users\a\transmission-telegram.conf`}, `C:\Users\a\transmission-telegram.conf`},
		{[]string{"-no-live", "--config", `C:\b.conf`}, `C:\b.conf`},
		{[]string{`-config=C:\c.conf`}, `C:\c.conf`},
		{[]string{`--config=C:\d.conf`}, `C:\d.conf`},
		{[]string{"-config"}, ""},
		{nil, ""},
	} {
		if got := configPathOf(test.args); got != test.want {
			t.Errorf("configPathOf(%q) = %q, want %q", test.args, got, test.want)
		}
	}
}
