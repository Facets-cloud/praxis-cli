package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/mcpmanifest"
	"github.com/Facets-cloud/praxis-cli/internal/skillcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
)

// stubMCPManifestFetch swaps mcpmanifest.Fetch with a no-network stub for
// the duration of the test. Returns a tiny but valid manifest body so
// runPostAuthSetup's snapshot step still exercises WriteSnapshot. Used
// by every test in this file so we never hit the real network.
func stubMCPManifestFetch(t *testing.T) {
	t.Helper()
	orig := mcpmanifest.Fetch
	mcpmanifest.Fetch = func(_ string, _ string, _ time.Duration) ([]byte, error) {
		return []byte(`{"mcps":{}}`), nil
	}
	t.Cleanup(func() { mcpmanifest.Fetch = orig })
}

// TestRunPostAuthSetup_CatalogFetchFailure_PreservesExisting pins the
// CodeRabbit-reviewed contract for v0.7: when the catalog fetch fails
// after auth has succeeded, the existing on-disk org skills MUST be
// left intact. A transient network blip should not turn `praxis login`
// into a destructive operation.
func TestRunPostAuthSetup_CatalogFetchFailure_PreservesExisting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	stubMCPManifestFetch(t)

	// Pre-seed: install a praxis-* skill on disk to represent a working
	// previous-profile setup. If the failure path wipes them, this test
	// will catch it.
	hosts := []harness.Harness{
		{Name: "claude-code", SkillDir: t.TempDir(), Detected: true},
	}
	if _, err := skillinstall.InstallWithBody("praxis-existing-skill", "body", hosts); err != nil {
		t.Fatalf("seed install: %v", err)
	}

	// Stub the seams so runPostAuthSetup sees: hosts present, meta-skill
	// installs OK, but fetchCatalog returns an error.
	origDetect, origInstall, origFetch := detectHarnesses, installSkill, fetchCatalog
	detectHarnesses = func() []harness.Harness { return hosts }
	installSkill = func(name string, h []harness.Harness) ([]skillinstall.Installation, error) {
		return []skillinstall.Installation{{SkillName: name, Harness: "claude-code", Path: "/x"}}, nil
	}
	fetchCatalog = func(baseURL, token string) ([]skillcatalog.Skill, error) {
		return nil, errors.New("simulated network failure")
	}
	t.Cleanup(func() {
		detectHarnesses, installSkill, fetchCatalog = origDetect, origInstall, origFetch
	})

	var buf bytes.Buffer
	state := runPostAuthSetup(&buf, false, "https://x.test", "tok")

	// Existing praxis-* installs must still be in the receipt — the
	// fetch failure must not have triggered UninstallByPrefix.
	got, err := skillinstall.List()
	if err != nil {
		t.Fatalf("skillinstall.List() unexpected error: %v", err)
	}
	found := false
	for _, e := range got {
		if e.SkillName == "praxis-existing-skill" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("praxis-existing-skill must survive fetch failure; List() = %+v", got)
	}

	// The state struct should report no removed skills (because we
	// never reached the wipe step) and no catalog skills installed.
	if len(state.removedSkills) != 0 {
		t.Errorf("removedSkills should be empty on fetch failure; got %d", len(state.removedSkills))
	}
	if len(state.catalogSkills) != 0 {
		t.Errorf("catalogSkills should be empty on fetch failure; got %d", len(state.catalogSkills))
	}
	// User-facing warning should mention the fetch failed and skills
	// were preserved.
	if !strings.Contains(buf.String(), "catalog fetch failed") {
		t.Errorf("output missing fetch-failure warning: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "left in place") {
		t.Errorf("output should reassure user existing skills are kept: %s", buf.String())
	}
}

// TestRunPostAuthSetup_NoHosts_StillRefreshesSnapshot pins the second
// CodeRabbit fix: when no AI hosts are detected, runPostAuthSetup must
// continue past Step 1 to refresh the MCP manifest snapshot, since the
// snapshot is useful even without an AI host installed.
func TestRunPostAuthSetup_NoHosts_StillRefreshesSnapshot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	stubMCPManifestFetch(t)

	origDetect := detectHarnesses
	detectHarnesses = func() []harness.Harness { return nil }
	t.Cleanup(func() { detectHarnesses = origDetect })

	var buf bytes.Buffer
	state := runPostAuthSetup(&buf, false, "https://x.test", "tok")

	// Friendly message but flow continues.
	if !strings.Contains(buf.String(), "No supported AI hosts") {
		t.Errorf("output missing no-hosts notice: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "Continuing") {
		t.Errorf("output should reassure user the flow continues: %s", buf.String())
	}

	// Manifest snapshot step ran — we expect either a path or a warning,
	// never both empty (which would mean we returned early without
	// attempting it).
	if state.snapshotPath == "" && state.snapshotWarning == "" {
		t.Error("manifest snapshot step should have run (path or warning expected)")
	}
}
