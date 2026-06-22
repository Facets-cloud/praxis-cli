package paths

import (
	"os"
	"path/filepath"
	"strings"
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

// mkProjectRoot creates dir/.praxis and returns the marker path.
func mkProjectRoot(t *testing.T, dir string) string {
	t.Helper()
	marker := filepath.Join(dir, ".praxis")
	if err := os.MkdirAll(marker, 0o700); err != nil {
		t.Fatalf("mkdir marker: %v", err)
	}
	return marker
}

func TestProjectRoot_FoundInCwd(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "work", "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := mkProjectRoot(t, repo)
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))

	got, ok, err := ProjectRoot()
	if err != nil || !ok {
		t.Fatalf("ProjectRoot() = %q, ok=%v, err=%v; want found", got, ok, err)
	}
	if got != marker {
		t.Errorf("ProjectRoot() = %q, want %q", got, marker)
	}
}

func TestProjectRoot_FoundInAncestor(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "work", "repo")
	sub := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := mkProjectRoot(t, repo)
	t.Cleanup(SetGetwdForTest(func() (string, error) { return sub, nil }))

	got, ok, _ := ProjectRoot()
	if !ok || got != marker {
		t.Errorf("ProjectRoot() = %q, ok=%v; want %q (walk up to ancestor)", got, ok, marker)
	}
}

func TestProjectRoot_StopsBelowHome_NeverReturnsHomeDotPraxis(t *testing.T) {
	home := withHome(t)
	// The global ~/.praxis exists, and cwd is a plain dir under home with no
	// project marker. Discovery must NOT mistake ~/.praxis for a project root.
	mkProjectRoot(t, home) // creates ~/.praxis
	repo := filepath.Join(home, "work", "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))

	if got, ok, _ := ProjectRoot(); ok {
		t.Errorf("ProjectRoot() = %q, ok=true; want not-found (must not return ~/.praxis)", got)
	}
}

func TestProjectRoot_CwdOutsideHome_NotFound(t *testing.T) {
	withHome(t)
	outside := t.TempDir() // a sibling temp dir, not under the faked home
	mkProjectRoot(t, outside)
	t.Cleanup(SetGetwdForTest(func() (string, error) { return outside, nil }))

	if got, ok, _ := ProjectRoot(); ok {
		t.Errorf("ProjectRoot() = %q, ok=true; want not-found for cwd outside home", got)
	}
}

func TestActiveRoot_ProjectThenHomeThenOverride(t *testing.T) {
	home := withHome(t)
	homeRoot := filepath.Join(home, ".praxis")
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	// No project marker → home root.
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))
	if got, _ := ActiveRoot(); got != homeRoot {
		t.Errorf("ActiveRoot() with no project = %q, want %q", got, homeRoot)
	}

	// Project marker present → project root.
	marker := mkProjectRoot(t, repo)
	if got, _ := ActiveRoot(); got != marker {
		t.Errorf("ActiveRoot() with project = %q, want %q", got, marker)
	}

	// Override wins over everything.
	restore := OverrideActiveRoot(homeRoot)
	if !RootIsPinned() {
		t.Error("RootIsPinned() = false after OverrideActiveRoot")
	}
	if got, _ := ActiveRoot(); got != homeRoot {
		t.Errorf("ActiveRoot() with override = %q, want %q", got, homeRoot)
	}
	restore()
	if RootIsPinned() {
		t.Error("RootIsPinned() = true after restore")
	}
}

func TestInstalledAndMCPTools_FollowActiveRoot(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := mkProjectRoot(t, repo)
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))

	inst, _ := Installed()
	if want := filepath.Join(marker, "installed.json"); inst != want {
		t.Errorf("Installed() = %q, want %q (project-local)", inst, want)
	}
	mcp, _ := MCPTools()
	if want := filepath.Join(marker, "mcp-tools.json"); mcp != want {
		t.Errorf("MCPTools() = %q, want %q (project-local)", mcp, want)
	}
}

func TestCredentials_AlwaysHome_EvenInProject(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	mkProjectRoot(t, repo)
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))

	got, _ := Credentials()
	if want := filepath.Join(home, ".praxis", "credentials"); got != want {
		t.Errorf("Credentials() = %q, want %q (must stay global even in project)", got, want)
	}
}

func TestEnsureProjectRoot_CreatesUnderHome(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))

	root, err := EnsureProjectRoot()
	if err != nil {
		t.Fatalf("EnsureProjectRoot() err = %v", err)
	}
	if want := filepath.Join(repo, ".praxis"); root != want {
		t.Errorf("EnsureProjectRoot() = %q, want %q", root, want)
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		t.Errorf("EnsureProjectRoot() did not create dir: %v", err)
	}
}

func TestEnsureProjectRoot_RejectsOutsideHome(t *testing.T) {
	withHome(t)
	outside := t.TempDir()
	t.Cleanup(SetGetwdForTest(func() (string, error) { return outside, nil }))

	_, err := EnsureProjectRoot()
	if err == nil {
		t.Fatal("EnsureProjectRoot() should error for cwd outside home, got nil")
	}
	// The message carries the user-facing guidance — assert on it, not just non-nil.
	if !strings.Contains(err.Error(), "under your home directory") {
		t.Errorf("error should explain the home-subtree requirement; got %q", err.Error())
	}
}

func TestEnsureProjectRoot_RejectsHomeItself(t *testing.T) {
	home := withHome(t)
	t.Cleanup(SetGetwdForTest(func() (string, error) { return home, nil }))

	_, err := EnsureProjectRoot()
	if err == nil {
		t.Fatal("EnsureProjectRoot() should error when cwd is home itself, got nil")
	}
	if !strings.Contains(err.Error(), "under your home directory") {
		t.Errorf("error should explain the home-subtree requirement; got %q", err.Error())
	}
}

func TestProjectConfig_PathAndPresence(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(SetGetwdForTest(func() (string, error) { return repo, nil }))

	// No project root yet.
	if _, ok, _ := ProjectConfig(); ok {
		t.Error("ProjectConfig() ok=true with no project root")
	}
	marker := mkProjectRoot(t, repo)
	got, ok, err := ProjectConfig()
	if err != nil || !ok {
		t.Fatalf("ProjectConfig() ok=%v err=%v; want found", ok, err)
	}
	if want := filepath.Join(marker, "config.json"); got != want {
		t.Errorf("ProjectConfig() = %q, want %q", got, want)
	}
}
