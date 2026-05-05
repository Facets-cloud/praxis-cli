package paths

import (
	"os"
	"path/filepath"
	"testing"
)

// withHome temporarily redirects $HOME so the package's filesystem-derived
// helpers are deterministic.
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

func TestDir_NoHome(t *testing.T) {
	// Both HOME (Unix) and USERPROFILE (Windows) cleared so os.UserHomeDir()
	// errors. We don't assert on the exact error message — just that one is
	// returned and Dir doesn't paper over it.
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, err := Dir(); err == nil {
		t.Fatal("Dir() should error when home is unresolvable, got nil")
	}
}

func TestCredentials_UnderDotPraxis(t *testing.T) {
	home := withHome(t)
	got, err := Credentials()
	if err != nil {
		t.Fatalf("Credentials err = %v", err)
	}
	want := filepath.Join(home, ".praxis", "credentials")
	if got != want {
		t.Errorf("Credentials() = %q, want %q", got, want)
	}
}

// We don't write to disk in tests — Credentials() returns a path; actual
// reads/writes happen in the cmd layer.
var _ = os.Remove
