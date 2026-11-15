//go:build windows

package service

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

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

// The service's folder belongs to the administrators, and lets in only them
// and the system, whether it is made or found; what is made in it inherits
// that.
func TestSecureServiceDir(t *testing.T) {
	if !isAdmin() {
		t.Skip("needs an elevated prompt, as -install does")
	}
	dir := filepath.Join(t.TempDir(), "transmission-telegram")
	for range 2 { // made, then found
		if err := secureServiceDir(dir); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, filepath.Join(dir, "state.json")} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		if path == dir {
			owner, _, err := sd.Owner()
			if err != nil || !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
				t.Fatalf("%s belongs to %v (%v)", path, owner, err)
			}
			control, _, err := sd.Control()
			if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
				t.Fatalf("%s inherits the security of its parent: %s", path, sd)
			}
		}
		dacl, _, err := sd.DACL()
		if err != nil {
			t.Fatal(err)
		}
		if dacl.AceCount != 2 {
			t.Fatalf("%s lets in more than the system and the administrators: %s", path, sd)
		}
	}
}
