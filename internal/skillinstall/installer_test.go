package skillinstall

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
)

// fakeHosts builds a slice of Harnesses pointing at temp-dir skill paths,
// so tests don't touch the real ~/.claude or ~/.gemini.
func fakeHosts(t *testing.T) []harness.Harness {
	t.Helper()
	root := t.TempDir()
	return []harness.Harness{
		{Name: "claude-code", DisplayName: "Claude Code", Detected: true,
			SkillDir: filepath.Join(root, "claude", "skills")},
		{Name: "codex", DisplayName: "OpenAI Codex", Detected: true,
			SkillDir: filepath.Join(root, "agents", "skills")},
		{Name: "gemini-cli", DisplayName: "Gemini CLI", Detected: true,
			SkillDir: filepath.Join(root, "gemini", "skills")},
	}
}

func TestContentFor_PraxisExists(t *testing.T) {
	body, err := ContentFor("praxis")
	if err != nil {
		t.Fatalf("ContentFor('praxis') err = %v", err)
	}
	for _, want := range []string{"---", "name: praxis", "description:", "Praxis CLI"} {
		if !strings.Contains(body, want) {
			t.Errorf("dummy content missing %q", want)
		}
	}
}

func TestContentFor_UnknownSkill(t *testing.T) {
	_, err := ContentFor("release-debugging")
	if err == nil {
		t.Fatal("expected error for unknown skill, got nil")
	}
	if !strings.Contains(err.Error(), "release-debugging") {
		t.Errorf("err should mention the skill name, got %v", err)
	}
}

func TestInstall_WritesToEachHostAndUpdatesReceipt(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // receipt lives under HOME
	hosts := fakeHosts(t)

	results, err := Install("praxis", hosts)
	if err != nil {
		t.Fatalf("Install err = %v", err)
	}
	if len(results) != 3 {
		t.Errorf("got %d installs, want 3", len(results))
	}

	// Each host's SKILL.md should exist with the right content.
	for _, in := range results {
		body, err := os.ReadFile(in.Path)
		if err != nil {
			t.Errorf("read %s: %v", in.Path, err)
			continue
		}
		if !strings.Contains(string(body), "name: praxis") {
			t.Errorf("file %s missing skill content", in.Path)
		}
	}

	// Receipt should list all 3.
	recorded, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 3 {
		t.Errorf("List() = %d entries, want 3", len(recorded))
	}
}

func TestInstall_Idempotent_NoDuplicateReceiptEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)

	if _, err := Install("praxis", hosts); err != nil {
		t.Fatal(err)
	}
	if _, err := Install("praxis", hosts); err != nil {
		t.Fatal(err)
	}

	recorded, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 3 {
		t.Errorf("after second install, receipt has %d entries (want 3) — install isn't idempotent", len(recorded))
	}
}

func TestInstall_OnlySpecifiedHosts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)
	results, err := Install("praxis", hosts[:1]) // claude-code only
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("got %d installs, want 1", len(results))
	}
	if results[0].Harness != "claude-code" {
		t.Errorf("harness = %s, want claude-code", results[0].Harness)
	}
}

func TestUninstall_RemovesFilesAndReceiptEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)
	if _, err := Install("praxis", hosts); err != nil {
		t.Fatal(err)
	}

	removed, err := Uninstall("praxis")
	if err != nil {
		t.Fatalf("Uninstall err = %v", err)
	}
	if len(removed) != 3 {
		t.Errorf("Uninstall returned %d, want 3", len(removed))
	}

	for _, e := range removed {
		if _, err := os.Stat(e.Path); !os.IsNotExist(err) {
			t.Errorf("file %s should be gone, stat err = %v", e.Path, err)
		}
	}

	recorded, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 0 {
		t.Errorf("List() after uninstall = %d entries, want 0", len(recorded))
	}
}

// TestUninstallByPrefix_KeepsMetaWipesPrefixed pins the v0.7 invariant:
// `praxis logout` (and `praxis login` profile-switch) must wipe every
// org skill (praxis-* prefix) while leaving the meta-skill ("praxis"
// exactly, no suffix) intact.
func TestUninstallByPrefix_KeepsMetaWipesPrefixed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)

	// Meta-skill via Install (uses ContentFor) + two org skills via
	// InstallWithBody (mirrors how the catalog flow works).
	if _, err := Install("praxis", hosts); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallWithBody("praxis-k8s-operations", "k8s body", hosts); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallWithBody("praxis-cloud-operations", "cloud body", hosts); err != nil {
		t.Fatal(err)
	}

	removed, err := UninstallByPrefix("praxis-")
	if err != nil {
		t.Fatalf("UninstallByPrefix err = %v", err)
	}
	// 2 skills × 3 hosts = 6 entries removed.
	if len(removed) != 6 {
		t.Errorf("removed %d entries, want 6", len(removed))
	}
	for _, e := range removed {
		if _, err := os.Stat(e.Path); !os.IsNotExist(err) {
			t.Errorf("org skill file %s should be gone", e.Path)
		}
	}

	// Meta-skill must survive. Verify via List() and via the on-disk file.
	recorded, err := List()
	if err != nil {
		t.Fatalf("List() err = %v", err)
	}
	if len(recorded) != 3 {
		t.Errorf("List() = %d entries after wipe, want 3 (meta-skill x 3 hosts)", len(recorded))
	}
	for _, e := range recorded {
		if e.SkillName != "praxis" {
			t.Errorf("survivor entry has skill_name=%q, want 'praxis'", e.SkillName)
		}
		if _, err := os.Stat(e.Path); err != nil {
			t.Errorf("meta-skill file %s should still exist: %v", e.Path, err)
		}
	}
}

// TestUninstallByPrefix_KeepsPrefixShapedMetaSkill pins the invariant
// that prefix-shaped meta-skills (e.g. "praxis-memory") survive the
// "praxis-" wipe via the IsMetaSkill exclusion. Without this guard, a
// profile switch would silently remove the memory meta-skill and break
// the next session's `praxis memory recall` discoverability.
func TestUninstallByPrefix_KeepsPrefixShapedMetaSkill(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)

	// Both binary-embedded meta-skills + one org skill.
	if _, err := Install("praxis", hosts); err != nil {
		t.Fatal(err)
	}
	if _, err := Install("praxis-memory", hosts); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallWithBody("praxis-k8s-operations", "k8s body", hosts); err != nil {
		t.Fatal(err)
	}

	removed, err := UninstallByPrefix("praxis-")
	if err != nil {
		t.Fatalf("UninstallByPrefix err = %v", err)
	}
	// Only "praxis-k8s-operations" should be wiped — 1 skill × 3 hosts.
	if len(removed) != 3 {
		t.Errorf("removed %d entries; want 3 (only the org skill, across 3 hosts)", len(removed))
	}
	for _, e := range removed {
		if e.SkillName != "praxis-k8s-operations" {
			t.Errorf("unexpected removal: %q", e.SkillName)
		}
	}

	// Both meta-skills must survive — 2 meta × 3 hosts = 6 entries.
	recorded, err := List()
	if err != nil {
		t.Fatalf("List() err = %v", err)
	}
	if len(recorded) != 6 {
		t.Errorf("List() = %d entries after wipe; want 6 (2 meta-skills x 3 hosts)", len(recorded))
	}
	survivors := map[string]int{}
	for _, e := range recorded {
		survivors[e.SkillName]++
		if _, err := os.Stat(e.Path); err != nil {
			t.Errorf("meta-skill file %s should still exist: %v", e.Path, err)
		}
	}
	if survivors["praxis"] != 3 || survivors["praxis-memory"] != 3 {
		t.Errorf("expected 3 of each meta-skill; got %+v", survivors)
	}
}

func TestIsMetaSkill_CoversAllEmbedded(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"praxis", true},
		{"praxis-memory", true},
		{"praxis-k8s-operations", false},
		{"random-skill", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsMetaSkill(tt.name); got != tt.want {
				t.Errorf("IsMetaSkill(%q) = %v; want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestMetaSkillNames_ReturnsAllEmbedded(t *testing.T) {
	names := MetaSkillNames()
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	for _, want := range []string{"praxis", "praxis-memory"} {
		if !have[want] {
			t.Errorf("MetaSkillNames() missing %q; got %v", want, names)
		}
	}
}

func TestContentFor_PraxisMemory_HasFrontmatterAndBody(t *testing.T) {
	body, err := ContentFor("praxis-memory")
	if err != nil {
		t.Fatalf("ContentFor: %v", err)
	}
	for _, want := range []string{
		"name: praxis-memory",
		"description:",
		"praxis memory recall",
		"praxis memory add",
		"Exit codes",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("praxis-memory body missing %q", want)
		}
	}
}

func TestUninstallByPrefix_RejectsEmptyPrefix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := UninstallByPrefix(""); err == nil {
		t.Error("expected error for empty prefix")
	}
}

func TestUninstall_UnknownSkill_NoOp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	removed, err := Uninstall("never-installed")
	if err != nil {
		t.Errorf("Uninstall of nothing should not error: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("Uninstall returned %d, want 0", len(removed))
	}
}

func TestList_FreshHome_EmptyNotError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := List()
	if err != nil {
		t.Errorf("List() on fresh home = err %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("List() = %d entries, want 0", len(got))
	}
}

func TestRefresh_RewritesEachInstalledFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)
	if _, err := Install("praxis", hosts); err != nil {
		t.Fatal(err)
	}

	// Tamper with one of the installed files to simulate stale content.
	entries, _ := List()
	if err := os.WriteFile(entries[0].Path, []byte("STALE"), 0600); err != nil {
		t.Fatal(err)
	}

	refreshed, err := Refresh()
	if err != nil {
		t.Fatalf("Refresh err = %v", err)
	}
	if len(refreshed) != 3 {
		t.Errorf("Refresh returned %d, want 3", len(refreshed))
	}

	body, err := os.ReadFile(entries[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) == "STALE" {
		t.Errorf("file %s still contains STALE — Refresh did not rewrite", entries[0].Path)
	}
	if !strings.Contains(string(body), "name: praxis") {
		t.Errorf("file content missing skill body after refresh")
	}
}

func TestRefresh_EmptyReceipt_NoOp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := Refresh()
	if err != nil {
		t.Errorf("Refresh on empty receipt should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Refresh on empty receipt returned %d entries, want 0", len(got))
	}
}

func TestRefresh_SkipsUnknownSkill(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)
	if _, err := Install("praxis", hosts); err != nil {
		t.Fatal(err)
	}

	// Manually inject a phantom installation for a skill that doesn't exist
	// in the dummy catalog. Refresh should skip it without erroring.
	receipt, _ := loadReceipt()
	receipt.Skills = append(receipt.Skills, Installation{
		SkillName: "phantom-skill",
		Harness:   "claude-code",
		Path:      filepath.Join(t.TempDir(), "phantom", "SKILL.md"),
	})
	if err := saveReceipt(receipt); err != nil {
		t.Fatal(err)
	}

	refreshed, err := Refresh()
	if err != nil {
		t.Fatalf("Refresh err = %v", err)
	}
	// 3 real praxis entries refreshed; phantom skipped.
	if len(refreshed) != 3 {
		t.Errorf("Refresh returned %d, want 3 (phantom should be skipped)", len(refreshed))
	}
}

func TestSaveReceipt_AtomicWrite_SurvivesParseRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)
	if _, err := Install("praxis", hosts); err != nil {
		t.Fatal(err)
	}

	// Read the on-disk JSON directly and verify it round-trips.
	r, err := loadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	var roundTripped Receipt
	if err := json.Unmarshal(raw, &roundTripped); err != nil {
		t.Errorf("receipt JSON does not round-trip: %v", err)
	}
}
