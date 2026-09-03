package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

// seedPAT writes a control-plane PAT profile — the kind that lives in the
// shared store and can be pinned to a tree.
func seedPAT(t *testing.T, name, url, token string) {
	t.Helper()
	if err := credentials.Put(name, credentials.FacetsProfile(url, "u@x", token)); err != nil {
		t.Fatalf("seed PAT %q: %v", name, err)
	}
}

// onDiskActiveURL is the control plane a bare command would use from here —
// the store's own answer, ignoring flag and environment.
func onDiskActiveURL(t *testing.T) string {
	t.Helper()
	store, err := credentials.Load()
	if err != nil {
		t.Fatal(err)
	}
	return store[credentials.OnDiskActiveName()].URL
}

// inDir points both discovery walks (paths, credentials) at dir for the test.
func inDir(t *testing.T, dir string) {
	t.Helper()
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return dir, nil }))
	t.Cleanup(credentials.SetGetwdForTest(func() (string, error) { return dir, nil }))
}

// homeFacetsURL reads the home store's [default] control plane directly, so a
// test inside a local tree can still assert the home file was left alone.
func homeFacetsURL(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".facets", "credentials"))
	if err != nil {
		return ""
	}
	return credentials.ParseRawINI(data)["default"]["control_plane_url"]
}
