package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/agentcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/agentinstall"
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
	state := runPostAuthSetup(&buf, false, "https://x.test", "tok", false)

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
	state := runPostAuthSetup(&buf, false, "https://x.test", "tok", false)

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

// TestRunPostAuthSetup_ProjectScope_WritesIntoProjectDir pins the core
// of the new project-level install option: when projectScoped is true,
// skill files land under the current working directory's host dirs
// (e.g. <cwd>/.claude/skills) rather than the global user-level
// ~/.claude/skills.
func TestRunPostAuthSetup_ProjectScope_WritesIntoProjectDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PRAXIS_PROFILE", "")
	stubMCPManifestFetch(t)

	proj := t.TempDir()
	origGetwd := getwd
	getwd = func() (string, error) { return proj, nil }
	t.Cleanup(func() { getwd = origGetwd })

	origDetect := detectHarnesses
	detectHarnesses = func() []harness.Harness {
		return []harness.Harness{{
			Name:     "claude-code",
			Detected: true,
			SkillDir: filepath.Join(home, ".claude", "skills"),
			AgentDir: filepath.Join(home, ".claude", "agents"),
		}}
	}
	t.Cleanup(func() { detectHarnesses = origDetect })

	origFetchSk := fetchCatalog
	fetchCatalog = func(_, _ string) ([]skillcatalog.Skill, error) { return nil, nil }
	t.Cleanup(func() { fetchCatalog = origFetchSk })

	origFetchAg := fetchAgents
	fetchAgents = func(_, _ string) ([]agentcatalog.Agent, error) { return nil, nil }
	t.Cleanup(func() { fetchAgents = origFetchAg })

	var buf bytes.Buffer
	runPostAuthSetup(&buf, false, "http://x", "tok", true)

	// The "praxis" meta-skill must land in the PROJECT dir...
	projSkill := filepath.Join(proj, ".claude", "skills", "praxis", "SKILL.md")
	if _, err := os.Stat(projSkill); err != nil {
		t.Errorf("meta-skill should be installed under project dir %s: %v", projSkill, err)
	}
	// ...and NOT in the user-level home dir.
	homeSkill := filepath.Join(home, ".claude", "skills", "praxis", "SKILL.md")
	if _, err := os.Stat(homeSkill); err == nil {
		t.Errorf("project scope must not write meta-skill to home dir %s", homeSkill)
	}
}

// TestRunPostAuthSetup_ProjectScope_DoesNotWipeUserLevelInstall guards
// the dangerous interaction with the shared install receipt: a
// project-scoped refresh must NOT trigger the receipt-based wipe that
// would delete the user's existing global (user-level) skills.
func TestRunPostAuthSetup_ProjectScope_DoesNotWipeUserLevelInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PRAXIS_PROFILE", "")
	stubMCPManifestFetch(t)

	// Pre-seed a user-level org skill in the receipt + on disk.
	userHosts := []harness.Harness{{
		Name:     "claude-code",
		Detected: true,
		SkillDir: filepath.Join(home, ".claude", "skills"),
	}}
	seeded, err := skillinstall.InstallWithBody("praxis-userlevel", "body", userHosts)
	if err != nil {
		t.Fatalf("seed install: %v", err)
	}
	seededPath := seeded[0].Path

	proj := t.TempDir()
	origGetwd := getwd
	getwd = func() (string, error) { return proj, nil }
	t.Cleanup(func() { getwd = origGetwd })

	origDetect := detectHarnesses
	detectHarnesses = func() []harness.Harness {
		return []harness.Harness{{
			Name:     "claude-code",
			Detected: true,
			SkillDir: filepath.Join(home, ".claude", "skills"),
			AgentDir: filepath.Join(home, ".claude", "agents"),
		}}
	}
	t.Cleanup(func() { detectHarnesses = origDetect })

	origFetchSk := fetchCatalog
	fetchCatalog = func(_, _ string) ([]skillcatalog.Skill, error) { return nil, nil }
	t.Cleanup(func() { fetchCatalog = origFetchSk })

	origFetchAg := fetchAgents
	fetchAgents = func(_, _ string) ([]agentcatalog.Agent, error) { return nil, nil }
	t.Cleanup(func() { fetchAgents = origFetchAg })

	var buf bytes.Buffer
	runPostAuthSetup(&buf, false, "http://x", "tok", true)

	if _, err := os.Stat(seededPath); err != nil {
		t.Errorf("user-level skill must survive a project-scoped refresh, but %s is gone: %v", seededPath, err)
	}
}

// TestRunPostAuthSetup_ProjectScope_GetwdError_FallsBackToUserLevel pins
// the getwd-failure fallback contract (CodeRabbit PR #23 actionable #1):
// when projectScoped is requested but the working directory cannot be
// resolved, the install must degrade to a *genuine* user-level refresh.
// That means the receipt-based wipe of the previous profile's praxis-*
// skills MUST run (it's gated on effective scope, not the requested
// flag), and the effective scope reported back must be user-level.
func TestRunPostAuthSetup_ProjectScope_GetwdError_FallsBackToUserLevel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PRAXIS_PROFILE", "")
	stubMCPManifestFetch(t)

	// Pre-seed a previous-profile org skill in the receipt + on disk.
	userHosts := []harness.Harness{{
		Name:     "claude-code",
		Detected: true,
		SkillDir: filepath.Join(home, ".claude", "skills"),
	}}
	if _, err := skillinstall.InstallWithBody("praxis-prevprofile", "body", userHosts); err != nil {
		t.Fatalf("seed install: %v", err)
	}

	// getwd fails — this is the fallback trigger.
	origGetwd := getwd
	getwd = func() (string, error) { return "", errors.New("simulated getwd failure") }
	t.Cleanup(func() { getwd = origGetwd })

	origDetect := detectHarnesses
	detectHarnesses = func() []harness.Harness {
		return []harness.Harness{{
			Name:     "claude-code",
			Detected: true,
			SkillDir: filepath.Join(home, ".claude", "skills"),
			AgentDir: filepath.Join(home, ".claude", "agents"),
		}}
	}
	t.Cleanup(func() { detectHarnesses = origDetect })

	origFetchSk := fetchCatalog
	fetchCatalog = func(_, _ string) ([]skillcatalog.Skill, error) { return nil, nil }
	t.Cleanup(func() { fetchCatalog = origFetchSk })
	origFetchAg := fetchAgents
	fetchAgents = func(_, _ string) ([]agentcatalog.Agent, error) { return nil, nil }
	t.Cleanup(func() { fetchAgents = origFetchAg })

	var buf bytes.Buffer
	// Requested projectScoped=true, but getwd fails → effective user-level.
	state := runPostAuthSetup(&buf, false, "http://x", "tok", true)

	// Effective scope must be user-level so callers report the truth.
	if state.projectScoped {
		t.Errorf("effective scope must be user-level after getwd failure; state.projectScoped = true")
	}
	// The fallback message must be surfaced to the user.
	if !strings.Contains(buf.String(), "Falling back to user-level install") {
		t.Errorf("output missing fallback notice: %s", buf.String())
	}
	// The user-level wipe MUST have run — the previous profile's skill is
	// gone from the receipt. (Before the fix it survived because the wipe
	// gate stayed on the requested flag, not the effective scope.)
	listed, err := skillinstall.List()
	if err != nil {
		t.Fatalf("skillinstall.List(): %v", err)
	}
	for _, e := range listed {
		if e.SkillName == "praxis-prevprofile" {
			t.Errorf("user-level fallback must wipe previous-profile skill, but praxis-prevprofile survived; List() = %+v", listed)
		}
	}
}

func TestRunPostAuthSetupFetchesAndInstallsAgents(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	stubMCPManifestFetch(t)

	origDetect := detectHarnesses
	defer func() { detectHarnesses = origDetect }()
	detectHarnesses = func() []harness.Harness {
		return []harness.Harness{{
			Name:     "claude-code",
			Detected: true,
			SkillDir: filepath.Join(tmp, "claude", "skills"),
			AgentDir: filepath.Join(tmp, "claude", "agents"),
		}}
	}

	origFetchSk := fetchCatalog
	defer func() { fetchCatalog = origFetchSk }()
	fetchCatalog = func(_, _ string) ([]skillcatalog.Skill, error) { return nil, nil }

	origFetchAg := fetchAgents
	defer func() { fetchAgents = origFetchAg }()
	fetchAgents = func(_, _ string) ([]agentcatalog.Agent, error) {
		return []agentcatalog.Agent{
			{Name: "alpha", Description: "a", SystemPrompt: "b", IsActive: true, Kind: agentcatalog.KindAgent},
		}, nil
	}

	var buf bytes.Buffer
	state := runPostAuthSetup(&buf, false, "http://x", "tok", false)
	if len(state.agents) != 1 {
		t.Fatalf("want 1 agent installed, got %d", len(state.agents))
	}
	want := filepath.Join(tmp, "claude", "agents", "praxis-alpha.md")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("agent file should exist at %s: %v", want, err)
	}
}

func TestRunPostAuthSetupAgentFetchFailureLeavesExistingInPlace(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	stubMCPManifestFetch(t)

	hosts := []harness.Harness{{
		Name:     "claude-code",
		Detected: true,
		AgentDir: filepath.Join(tmp, "claude", "agents"),
	}}

	// Pre-seed: install an existing agent so the receipt + on-disk file
	// represent a working previous-profile state. If the fetch-failure
	// path destructively wipes them, the assertions below catch it.
	seeded, err := agentinstall.Install([]agentcatalog.Agent{{
		Name:         "preserve-me",
		Description:  "should survive a transient fetch failure",
		SystemPrompt: "seeded body",
		IsActive:     true,
		Kind:         agentcatalog.KindAgent,
	}}, hosts)
	if err != nil {
		t.Fatalf("seed install: %v", err)
	}
	if len(seeded) != 1 {
		t.Fatalf("seed install: want 1 entry, got %d", len(seeded))
	}
	seededPath := seeded[0].Path

	origDetect := detectHarnesses
	defer func() { detectHarnesses = origDetect }()
	detectHarnesses = func() []harness.Harness { return hosts }

	origFetchSk := fetchCatalog
	defer func() { fetchCatalog = origFetchSk }()
	fetchCatalog = func(_, _ string) ([]skillcatalog.Skill, error) { return nil, nil }

	origFetchAg := fetchAgents
	defer func() { fetchAgents = origFetchAg }()
	fetchAgents = func(_, _ string) ([]agentcatalog.Agent, error) {
		return nil, fmt.Errorf("simulated network failure")
	}

	var buf bytes.Buffer
	state := runPostAuthSetup(&buf, false, "http://x", "tok", false)

	// state.agents reports what THIS invocation installed; with a fetch
	// failure that should be empty — but the seeded agent must remain
	// in the receipt and on disk untouched.
	if len(state.agents) != 0 {
		t.Errorf("state.agents should be empty on fetch failure, got %d", len(state.agents))
	}

	// Receipt: the seeded entry must still be there.
	listed, err := agentinstall.List()
	if err != nil {
		t.Fatalf("list after runPostAuthSetup: %v", err)
	}
	found := false
	for _, e := range listed {
		if e.AgentName == "praxis-preserve-me" && e.Harness == "claude-code" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("seeded agent praxis-preserve-me missing from receipt after fetch failure; receipt now: %#v", listed)
	}

	// On-disk file: must still exist.
	if _, err := os.Stat(seededPath); err != nil {
		t.Errorf("seeded agent file %s should still exist after fetch failure: %v", seededPath, err)
	}
}
