package skillinstall

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// onboarding is the one embedded multi-file (tree) meta-skill today.
const onboardingSkill = "praxis-onboarding"

// use-ig is the Praxis-MCP read variant tree skill: it carries ig's graph
// query mental-model but routes every read through `praxis mcp ig`, so the
// host needs no local `ig`. Same bare name as ig's native skill (only one is
// ever present — ig ships its native copy only when praxis is absent).
const useIGSkill = "use-ig"

func TestMetaSkillNames_IncludesUseIG(t *testing.T) {
	names := MetaSkillNames()
	var found bool
	for _, n := range names {
		if n == useIGSkill {
			found = true
		}
	}
	if !found {
		t.Errorf("MetaSkillNames() = %v, want it to include %q", names, useIGSkill)
	}
	// Still sorted (login relies on deterministic order).
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("MetaSkillNames() not sorted: %v", names)
			break
		}
	}
}

func TestIsMetaSkill_UseIGPreserved(t *testing.T) {
	if !IsMetaSkill(useIGSkill) {
		t.Errorf("IsMetaSkill(%q) = false, want true (tree meta-skills must survive profile switch)", useIGSkill)
	}
}

// TestUseIGTreeSkill_IsMCPVariant guards that the embedded use-ig skill is the
// Praxis-MCP read variant: it queries the graph server-side via `praxis mcp
// ig` and does NOT carry the native local-`ig` read command surface.
func TestUseIGTreeSkill_IsMCPVariant(t *testing.T) {
	fsys, ok := treeSkillFS(useIGSkill)
	if !ok {
		t.Fatalf("treeSkillFS(%q) not found; use-ig must be an embedded tree skill", useIGSkill)
	}
	raw, err := fs.ReadFile(fsys, "SKILL.md")
	if err != nil {
		t.Fatalf("read use-ig SKILL.md: %v", err)
	}
	body := string(raw)

	if !strings.Contains(body, "name: use-ig") {
		t.Errorf("use-ig SKILL.md missing frontmatter `name: use-ig`")
	}
	// Reads must route through the Praxis MCP.
	if !strings.Contains(body, "praxis mcp ig") {
		t.Errorf("use-ig SKILL.md must invoke reads via `praxis mcp ig` (MCP variant)")
	}
	// It must NOT carry the native local-`ig` read command surface. The
	// backticked `ig query` is the tell of the local-ig variant; this MCP
	// copy uses `praxis mcp ig ig_query` instead.
	if strings.Contains(body, "`ig query`") {
		t.Errorf("use-ig SKILL.md contains local-ig read command `ig query`; this must be the `praxis mcp ig` variant")
	}
	// It must teach the local-checkout memory: how the agent resolves a node's
	// repo-relative path to a real file and remembers where the member lives.
	// This is the read counterpart the `praxis ig hook` nudge relies on.
	if !strings.Contains(body, "ig-checkouts.json") {
		t.Errorf("use-ig SKILL.md must teach the ~/.praxis/ig-checkouts.json local-checkout memory")
	}
}

func TestMetaSkillNames_IncludesTreeSkill(t *testing.T) {
	names := MetaSkillNames()
	var found bool
	for _, n := range names {
		if n == onboardingSkill {
			found = true
		}
	}
	if !found {
		t.Errorf("MetaSkillNames() = %v, want it to include %q", names, onboardingSkill)
	}
	// Still sorted (login relies on deterministic order).
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("MetaSkillNames() not sorted: %v", names)
			break
		}
	}
}

func TestIsMetaSkill_TreeSkillPreserved(t *testing.T) {
	if !IsMetaSkill(onboardingSkill) {
		t.Errorf("IsMetaSkill(%q) = false, want true (tree skills must be preserved on profile switch)", onboardingSkill)
	}
}

func TestIsTreeSkill(t *testing.T) {
	if !isTreeSkill(onboardingSkill) {
		t.Errorf("isTreeSkill(%q) = false, want true", onboardingSkill)
	}
	if isTreeSkill("praxis") {
		t.Errorf("isTreeSkill(\"praxis\") = true, want false (single-file meta-skill)")
	}
}

func TestInstall_TreeSkill_WritesWholeTree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)

	results, err := Install(onboardingSkill, hosts)
	if err != nil {
		t.Fatalf("Install(%q) err = %v", onboardingSkill, err)
	}
	if len(results) != len(hosts) {
		t.Fatalf("got %d installs, want %d", len(results), len(hosts))
	}

	for _, in := range results {
		// Canonical recorded path is the SKILL.md at the tree root.
		if filepath.Base(in.Path) != "SKILL.md" {
			t.Errorf("recorded path %q should point at SKILL.md", in.Path)
		}
		skillDir := filepath.Dir(in.Path)

		// SKILL.md present, de-templated, with the right frontmatter.
		skill, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
		if err != nil {
			t.Errorf("read SKILL.md: %v", err)
			continue
		}
		if !strings.Contains(string(skill), `name: "praxis-onboarding"`) {
			t.Errorf("SKILL.md missing frontmatter name in %s", skillDir)
		}
		if strings.Contains(string(skill), "{{BRAND_NAME}}") {
			t.Errorf("embedded SKILL.md still contains untemplated {{BRAND_NAME}}")
		}

		// The flow file must come along in its subdir.
		flow, err := os.ReadFile(filepath.Join(skillDir, "flows", "first-deployment.md"))
		if err != nil {
			t.Errorf("read flows/first-deployment.md: %v", err)
			continue
		}
		if !strings.Contains(string(flow), "import project-type --managed") {
			t.Errorf("flow file missing the load-bearing import command")
		}
	}
}

func TestInstallTree_PrunesStaleFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)
	results, err := Install(onboardingSkill, hosts)
	if err != nil {
		t.Fatalf("Install err = %v", err)
	}

	// Simulate a previous binary version that shipped a flow file this binary
	// no longer carries. Drop an orphan into each installed tree.
	for _, in := range results {
		stale := filepath.Join(filepath.Dir(in.Path), "flows", "retired-flow.md")
		if err := os.WriteFile(stale, []byte("stale"), 0600); err != nil {
			t.Fatalf("seed stale file: %v", err)
		}
	}

	// Re-install (e.g. the user re-runs login). The orphan must not survive.
	if _, err := Install(onboardingSkill, hosts); err != nil {
		t.Fatalf("re-Install err = %v", err)
	}
	for _, in := range results {
		stale := filepath.Join(filepath.Dir(in.Path), "flows", "retired-flow.md")
		if _, statErr := os.Stat(stale); !os.IsNotExist(statErr) {
			t.Errorf("stale file %s survived re-install (stat err = %v); tree install must prune orphans", stale, statErr)
		}
		// The real files must still be there.
		if _, statErr := os.Stat(filepath.Join(filepath.Dir(in.Path), "SKILL.md")); statErr != nil {
			t.Errorf("SKILL.md missing after re-install: %v", statErr)
		}
	}
}

func TestRefresh_PrunesStaleTreeFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)
	results, err := Install(onboardingSkill, hosts)
	if err != nil {
		t.Fatalf("Install err = %v", err)
	}

	stale := filepath.Join(filepath.Dir(results[0].Path), "flows", "retired-flow.md")
	if err := os.WriteFile(stale, []byte("stale"), 0600); err != nil {
		t.Fatalf("seed stale file: %v", err)
	}

	if _, err := Refresh(); err != nil {
		t.Fatalf("Refresh err = %v", err)
	}
	if _, statErr := os.Stat(stale); !os.IsNotExist(statErr) {
		t.Errorf("stale file %s survived Refresh (stat err = %v); tree refresh must prune orphans", stale, statErr)
	}
	// The real flow file must still be present after refresh.
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(results[0].Path), "flows", "first-deployment.md")); statErr != nil {
		t.Errorf("flow file missing after refresh: %v", statErr)
	}
}

func TestUninstall_TreeSkill_RemovesWholeTree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)
	if _, err := Install(onboardingSkill, hosts); err != nil {
		t.Fatalf("Install err = %v", err)
	}

	removed, err := Uninstall(onboardingSkill)
	if err != nil {
		t.Fatalf("Uninstall err = %v", err)
	}
	if len(removed) != len(hosts) {
		t.Errorf("removed %d, want %d", len(removed), len(hosts))
	}
	for _, r := range removed {
		skillDir := filepath.Dir(r.Path)
		if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
			t.Errorf("tree dir %s should be gone, stat err = %v", skillDir, err)
		}
	}
}

func TestUninstallByPrefix_PreservesTreeSkill(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)
	if _, err := Install(onboardingSkill, hosts); err != nil {
		t.Fatalf("Install err = %v", err)
	}

	// Simulate a profile switch wiping the praxis- org-skill namespace.
	if _, err := UninstallByPrefix("praxis-"); err != nil {
		t.Fatalf("UninstallByPrefix err = %v", err)
	}

	// The embedded tree skill must survive — files still on disk and still
	// tracked in the receipt.
	got, err := List()
	if err != nil {
		t.Fatalf("List err = %v", err)
	}
	var stillTracked bool
	for _, in := range got {
		if in.SkillName == onboardingSkill {
			stillTracked = true
			if _, statErr := os.Stat(in.Path); statErr != nil {
				t.Errorf("preserved skill file missing: %v", statErr)
			}
		}
	}
	if !stillTracked {
		t.Errorf("praxis-onboarding was wiped by UninstallByPrefix; tree meta-skills must be preserved")
	}
}

func TestRefresh_RewritesTreeSkill(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)
	results, err := Install(onboardingSkill, hosts)
	if err != nil {
		t.Fatalf("Install err = %v", err)
	}

	// Corrupt one host's SKILL.md, then Refresh should restore it.
	target := results[0].Path
	if err := os.WriteFile(target, []byte("corrupted"), 0600); err != nil {
		t.Fatalf("corrupt write: %v", err)
	}

	if _, err := Refresh(); err != nil {
		t.Fatalf("Refresh err = %v", err)
	}

	restored, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after refresh: %v", err)
	}
	if !strings.Contains(string(restored), `name: "praxis-onboarding"`) {
		t.Errorf("Refresh did not restore tree skill SKILL.md content")
	}
	// And the sibling flow file should still be present.
	flow := filepath.Join(filepath.Dir(target), "flows", "first-deployment.md")
	if _, err := os.Stat(flow); err != nil {
		t.Errorf("flow file missing after refresh: %v", err)
	}
}
