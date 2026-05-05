package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withHome temporarily redirects $HOME (and on darwin/linux that's what
// os.UserHomeDir() reads) to a temp dir, so the package's filesystem
// operations are isolated.
func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestDir_BuildsUnderHome(t *testing.T) {
	home := withHome(t)
	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir err = %v", err)
	}
	want := filepath.Join(home, ".praxis")
	if got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestEnsure_CreatesDirWith0700(t *testing.T) {
	home := withHome(t)
	got, err := Ensure()
	if err != nil {
		t.Fatalf("Ensure err = %v", err)
	}
	if got != filepath.Join(home, ".praxis") {
		t.Errorf("Ensure() = %q, want under home", got)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat %s: %v", got, err)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", got)
	}
	if mode := info.Mode().Perm(); mode != 0700 {
		t.Errorf("perm = %o, want 0700", mode)
	}
}

func TestEnsure_Idempotent(t *testing.T) {
	withHome(t)
	if _, err := Ensure(); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(); err != nil {
		t.Errorf("second Ensure() should not fail: %v", err)
	}
}

func TestPaths_AllUnderDotPraxis(t *testing.T) {
	home := withHome(t)
	wantPrefix := filepath.Join(home, ".praxis") + string(filepath.Separator)

	tests := []struct {
		name string
		fn   func() (string, error)
		want string
	}{
		{"Config", Config, "config.json"},
		{"Credentials", Credentials, "credentials"},
		{"Installed", Installed, "installed.json"},
		{"InstallReceipt", InstallReceipt, "install.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.fn()
			if err != nil {
				t.Fatalf("%s err = %v", tt.name, err)
			}
			if !strings.HasPrefix(got, wantPrefix) {
				t.Errorf("%s = %q, want prefix %q", tt.name, got, wantPrefix)
			}
			if filepath.Base(got) != tt.want {
				t.Errorf("%s base = %q, want %q", tt.name, filepath.Base(got), tt.want)
			}
		})
	}
}
