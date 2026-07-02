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
	"github.com/Facets-cloud/praxis-cli/internal/paths"
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

// pinProjectRoot creates <proj>/.praxis and pins it as the active root for
// the test, mirroring what login --local / refresh-skills --project do before
// calling runPostAuthSetup. Returns the project dir.
func pinProjectRoot(t *testing.T, home string) string {
	t.Helper()
	proj := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(proj, ".praxis"), 0o755); err != nil {
		t.Fatalf("mkdir proj/.praxis: %v", err)
	}
	t.Cleanup(paths.OverrideActiveRoot(filepath.Join(proj, ".praxis")))
	return proj
}

// TestRunPostAuthSetup_ProjectScope_WritesIntoProjectDir pins the core
// of project-level install: when the active root is a project root, skill
// files land under that repo's host dirs (e.g. <repo>/.claude/skills) rather
// than the global user-level ~/.claude/skills.
func TestRunPostAuthSetup_ProjectScope_WritesIntoProjectDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubMCPManifestFetch(t)

	proj := pinProjectRoot(t, home)

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
	state := runPostAuthSetup(&buf, false, "http://x", "tok")
	if !state.projectScoped {
		t.Errorf("expected project scope when active root is a project root")
	}

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

// TestRunPostAuthSetup_ProjectScope_DoesNotWipeUserLevelInstall guards the
// interaction with the install receipt: a project-scoped run reads/writes the
// PROJECT receipt, so it must NOT delete the user's existing global skills.
func TestRunPostAuthSetup_ProjectScope_DoesNotWipeUserLevelInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubMCPManifestFetch(t)

	// Pre-seed a user-level org skill in the GLOBAL receipt + on disk
	// (no active-root override yet → Installed() resolves to home).
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

	pinProjectRoot(t, home)

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
	runPostAuthSetup(&buf, false, "http://x", "tok")

	if _, err := os.Stat(seededPath); err != nil {
		t.Errorf("user-level skill must survive a project-scoped refresh, but %s is gone: %v", seededPath, err)
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
	state := runPostAuthSetup(&buf, false, "http://x", "tok")
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
		// SkillDir must point under tmp: runPostAuthSetup installs the
		// meta-skills before the agent step, and a host without a SkillDir
		// resolves to a CWD-relative path, leaking praxis* skill dirs into cmd/.
		SkillDir: filepath.Join(tmp, "claude", "skills"),
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
	state := runPostAuthSetup(&buf, false, "http://x", "tok")

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

// TestInstallFetchedCatalog_RoutesMultiFileToTree verifies the install branch:
// single-file skills go through installSkillBody (one SKILL.md), multi-file
// skills go through installSkillTree with their supporting files attached.
func TestInstallFetchedCatalog_RoutesMultiFileToTree(t *testing.T) {
	hosts := []harness.Harness{{Name: "claude-code", SkillDir: t.TempDir(), Detected: true}}

	var bodyCalls, treeCalls []string
	var treeFiles []skillinstall.FileBody
	origBody, origTree := installSkillBody, installSkillTree
	installSkillBody = func(name, _ string, _ []harness.Harness) ([]skillinstall.Installation, error) {
		bodyCalls = append(bodyCalls, name)
		return []skillinstall.Installation{{SkillName: name, Harness: "claude-code", Path: "/x/SKILL.md"}}, nil
	}
	installSkillTree = func(name, _ string, files []skillinstall.FileBody, _ []harness.Harness) ([]skillinstall.Installation, error) {
		treeCalls = append(treeCalls, name)
		treeFiles = files
		return []skillinstall.Installation{{SkillName: name, Harness: "claude-code", Path: "/x/SKILL.md"}}, nil
	}
	t.Cleanup(func() { installSkillBody, installSkillTree = origBody, origTree })

	skills := []skillcatalog.Skill{
		{Name: "plain", Content: "---\nname: plain\n---\nbody"},
		{
			Name:    "gcp",
			Content: "---\nname: gcp\n---\nbody",
			Files:   []skillcatalog.SkillFile{{Path: "catalog.md", Content: "c"}},
		},
	}

	var buf bytes.Buffer
	installFetchedCatalog(&buf, false, skills, hosts)

	if len(bodyCalls) != 1 || bodyCalls[0] != "praxis-plain" {
		t.Errorf("single-file should route to installSkillBody; got %v", bodyCalls)
	}
	if len(treeCalls) != 1 || treeCalls[0] != "praxis-gcp" {
		t.Errorf("multi-file should route to installSkillTree; got %v", treeCalls)
	}
	if len(treeFiles) != 1 || treeFiles[0].Path != "catalog.md" {
		t.Errorf("tree install should receive supporting files; got %v", treeFiles)
	}
}

// TestRunPostAuthSetup_EndToEnd_NoGeminiConflict is the full inside-out proof
// for issue #9. It drives the REAL runPostAuthSetup — real harness detection,
// real meta-skill embed install, real catalog install — on a simulated
// Codex+Gemini machine. Only the network seams (catalog/agents/MCP) are
// stubbed. It then asserts the exact condition Gemini CLI warns on — the same
// skill name resolving from BOTH ~/.gemini/skills and ~/.agents/skills — is
// never created: every praxis skill lives only at the shared alias, and
// nothing praxis-managed is written to ~/.gemini/skills.
func TestRunPostAuthSetup_EndToEnd_NoGeminiConflict(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "") // detection is dir-based only — deterministic, ignores the dev machine
	stubMCPManifestFetch(t)

	geminiSkills := filepath.Join(home, ".gemini", "skills")
	agentsSkills := filepath.Join(home, ".agents", "skills")

	// Simulate a machine with BOTH Codex (~/.codex) and Gemini CLI (~/.gemini).
	mustMkdir(t, filepath.Join(home, ".codex"))
	mustMkdir(t, filepath.Join(home, ".gemini"))

	// A skill the user authored by hand in Gemini's own dir — praxis must never
	// touch it (we only ever write to the shared alias now).
	seedSkillFile(t, geminiSkills, "user-own")

	// Confirm the premise: real detection sees Codex + Gemini sharing the alias.
	det := harness.Detected()
	byName := map[string]harness.Harness{}
	for _, h := range det {
		byName[h.Name] = h
	}
	if _, ok := byName["codex"]; !ok {
		t.Fatalf("premise broken: Codex not detected; got %v", det)
	}
	if _, ok := byName["gemini-cli"]; !ok {
		t.Fatalf("premise broken: Gemini not detected; got %v", det)
	}
	if byName["codex"].SkillDir != agentsSkills || byName["gemini-cli"].SkillDir != agentsSkills {
		t.Fatalf("premise broken: codex=%q gemini=%q, both want %q",
			byName["codex"].SkillDir, byName["gemini-cli"].SkillDir, agentsSkills)
	}

	// Stub only the network seams. Catalog returns one real single-file skill;
	// agents empty. Install/detection/migration all run for real.
	origFetch, origAgents := fetchCatalog, fetchAgents
	fetchCatalog = func(_, _ string) ([]skillcatalog.Skill, error) {
		return []skillcatalog.Skill{{Name: "cloudops", Content: "---\nname: cloudops\n---\nbody"}}, nil
	}
	fetchAgents = func(_, _ string) ([]agentcatalog.Agent, error) { return nil, nil }
	t.Cleanup(func() { fetchCatalog, fetchAgents = origFetch, origAgents })

	var buf bytes.Buffer
	runPostAuthSetup(&buf, false, "https://x.test", "tok")

	// 1. The catalog skill and both metas installed at the shared alias.
	for _, name := range []string{"praxis-cloudops", "praxis", "praxis-memory"} {
		if _, err := os.Stat(filepath.Join(agentsSkills, name, "SKILL.md")); err != nil {
			t.Errorf("%s not installed at the alias %s: %v", name, agentsSkills, err)
		}
	}

	// 2. THE INVARIANT: nothing praxis-managed may remain under ~/.gemini/skills —
	// that dir is the second location Gemini scans, so any praxis-* there is a
	// conflict source. The user's own skill must still be present.
	remaining := listDirNames(t, geminiSkills)
	for _, name := range remaining {
		if name == "praxis" || strings.HasPrefix(name, "praxis-") {
			t.Errorf("praxis skill %q still in ~/.gemini/skills — Gemini would warn on it", name)
		}
	}
	if !sliceHas(remaining, "user-own") {
		t.Errorf("user's own skill was wrongly swept from ~/.gemini/skills; remaining=%v", remaining)
	}

	// 3. The precise Gemini conflict predicate: no praxis skill name appears in
	// BOTH scanned dirs at once.
	geminiSet := map[string]bool{}
	for _, n := range remaining {
		geminiSet[n] = true
	}
	for _, n := range listDirNames(t, agentsSkills) {
		if (n == "praxis" || strings.HasPrefix(n, "praxis-")) && geminiSet[n] {
			t.Errorf("skill %q resolves from BOTH ~/.agents/skills and ~/.gemini/skills — the exact #9 conflict", n)
		}
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
}

func seedSkillFile(t *testing.T, base, name string) {
	t.Helper()
	mustMkdir(t, filepath.Join(base, name))
	if err := os.WriteFile(filepath.Join(base, name, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func listDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func sliceHas(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
