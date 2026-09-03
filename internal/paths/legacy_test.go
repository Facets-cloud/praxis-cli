package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyConfig_UnderHomeRoot(t *testing.T) {
	home := withHome(t)
	got, err := LegacyConfig()
	if err != nil || got != filepath.Join(home, ".praxis", "config.json") {
		t.Errorf("LegacyConfig() = %q, %v", got, err)
	}
}

func TestLegacyProjectPointer_Cases(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "repo")
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(filepath.Join(repo, ".praxis"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// A pointer file with no profile line is still reported, with no name.
	if err := os.WriteFile(filepath.Join(repo, ".praxis", "config.json"), []byte("[default]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(SetGetwdForTest(func() (string, error) { return sub, nil }))
	path, profile, ok := LegacyProjectPointer()
	if !ok || profile != "" || path != filepath.Join(repo, ".praxis", "config.json") {
		t.Errorf("LegacyProjectPointer() = %q, %q, %v", path, profile, ok)
	}
	// Outside home nothing is reported.
	t.Cleanup(SetGetwdForTest(func() (string, error) { return t.TempDir(), nil }))
	if _, _, ok := LegacyProjectPointer(); ok {
		t.Error("LegacyProjectPointer() reported a pointer outside home")
	}
}

// A re-pin from a subdirectory of a pinned tree reuses the tree's root
// instead of nesting a second tree inside it.
func TestEnsureProjectRoot_ReusesAnAncestorRoot(t *testing.T) {
	home := withHome(t)
	repo := filepath.Join(home, "repo")
	sub := filepath.Join(repo, "services", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	root := mkProjectRoot(t, repo)
	t.Cleanup(SetGetwdForTest(func() (string, error) { return sub, nil }))
	got, err := EnsureProjectRoot()
	if err != nil || got != root {
		t.Errorf("EnsureProjectRoot() = %q, %v; want %q", got, err, root)
	}
	if _, err := os.Stat(filepath.Join(sub, ".praxis")); !os.IsNotExist(err) {
		t.Error("EnsureProjectRoot nested a second .praxis inside the pinned tree")
	}
}
