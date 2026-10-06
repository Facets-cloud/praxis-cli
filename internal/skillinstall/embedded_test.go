package skillinstall

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
)

func TestEmbeddedSkillNames(t *testing.T) {
	if got := MetaSkillNames(); !slices.Equal(got, []string{"praxis"}) {
		t.Errorf("MetaSkillNames() = %v, want [praxis]", got)
	}
	if got := BootstrapSkillNames(); !slices.Equal(got, []string{"praxis"}) {
		t.Errorf("BootstrapSkillNames() = %v, want [praxis]", got)
	}
	if !IsMetaSkill("praxis") {
		t.Error("IsMetaSkill(praxis) = false; profile switches would wipe it")
	}
	for _, name := range legacyBuiltinSkills {
		if IsMetaSkill(name) {
			t.Errorf("IsMetaSkill(%q) = true; replaced skills are no longer embedded", name)
		}
	}
	body, err := ContentFor("praxis")
	if err != nil || !strings.HasPrefix(body, "---\nname: praxis\n") {
		t.Errorf("ContentFor(praxis) = %.40q, %v", body, err)
	}
	if _, err := ContentFor("praxis-memory"); err == nil {
		t.Error("ContentFor(praxis-memory) resolved a removed skill")
	}
}

// Install writes every embedded file with its bytes, including hidden ones,
// and makes scripts executable.
func TestInstall_PraxisWritesWholeTree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)
	results, err := Install("praxis", hosts)
	if err != nil || len(results) != len(hosts) {
		t.Fatalf("Install = %d results, %v", len(results), err)
	}
	tree, _ := treeSkillFS("praxis")
	files := 0
	err = fs.WalkDir(tree, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files++
		want, _ := fs.ReadFile(tree, p)
		for _, h := range hosts {
			dst := filepath.Join(h.SkillDir, "praxis", filepath.FromSlash(p))
			got, err := os.ReadFile(dst)
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("%s: %v (equal=%t)", dst, err, bytes.Equal(got, want))
				continue
			}
			if strings.HasPrefix(p, "scripts/") {
				if fi, _ := os.Stat(dst); fi.Mode().Perm() != 0700 {
					t.Errorf("%s mode = %o, want 700", dst, fi.Mode().Perm())
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < 2 {
		t.Fatalf("embedded praxis tree has %d files", files)
	}
}

// Re-install and Refresh both drop files the binary no longer ships.
func TestPraxisTree_PrunesStaleFiles(t *testing.T) {
	for _, via := range []string{"install", "refresh"} {
		t.Run(via, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			hosts := fakeHosts(t)
			results, err := Install("praxis", hosts)
			if err != nil {
				t.Fatal(err)
			}
			stale := filepath.Join(filepath.Dir(results[0].Path), "references", "retired.md")
			if err := os.WriteFile(stale, []byte("stale"), 0600); err != nil {
				t.Fatal(err)
			}
			if via == "install" {
				_, err = Install("praxis", hosts)
			} else {
				_, err = Refresh()
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(stale); !os.IsNotExist(err) {
				t.Errorf("stale file survived %s: %v", via, err)
			}
		})
	}
}

func TestRefresh_RestoresPraxisTree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)
	results, err := Install("praxis", hosts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(results[0].Path, []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Refresh(); err != nil {
		t.Fatal(err)
	}
	want, _ := ContentFor("praxis")
	if got, _ := os.ReadFile(results[0].Path); string(got) != want {
		t.Error("Refresh did not restore SKILL.md")
	}
}

func TestUninstall_PraxisRemovesWholeTreeButPrefixWipeKeepsIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)
	if _, err := Install("praxis", hosts); err != nil {
		t.Fatal(err)
	}
	if _, err := UninstallByPrefix("praxis-"); err != nil {
		t.Fatal(err)
	}
	if got, _ := List(); len(got) != len(hosts) {
		t.Fatalf("a praxis- wipe removed the embedded skill: %v", got)
	}
	removed, err := Uninstall("praxis")
	if err != nil || len(removed) != len(hosts) {
		t.Fatalf("Uninstall = %d, %v", len(removed), err)
	}
	for _, r := range removed {
		if _, err := os.Stat(filepath.Dir(r.Path)); !os.IsNotExist(err) {
			t.Errorf("%s still exists: %v", filepath.Dir(r.Path), err)
		}
	}
}

// legacyInstall writes a replaced embedded skill the way an older praxis did
// and records it with the given receipt source.
func legacyInstall(t *testing.T, h harness.Harness, name, source string) Installation {
	t.Helper()
	dir := filepath.Join(h.SkillDir, name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("old "+name), 0600); err != nil {
		t.Fatal(err)
	}
	in := Installation{SkillName: name, Harness: h.Name, Path: filepath.Join(dir, "SKILL.md"), Source: source}
	if source != "" { // every recorded install also records its digest
		d, err := digestTree(dir)
		if err != nil {
			t.Fatal(err)
		}
		in.Digest = d
	}
	r, err := loadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	r.Skills = append(r.Skills, in)
	if err := saveReceipt(r); err != nil {
		t.Fatal(err)
	}
	return in
}

func TestRetireLegacyBuiltins(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	all := fakeHosts(t)
	claude, codex, gemini := all[0], all[1], all[2]
	gemini.SkillDir = codex.SkillDir // Codex and Gemini share ~/.agents/skills
	noPraxis := harness.Harness{Name: "antigravity", SkillDir: filepath.Join(t.TempDir(), "skills")}

	if _, err := Install("praxis", []harness.Harness{claude, codex, gemini}); err != nil {
		t.Fatal(err)
	}
	legacyInstall(t, claude, "praxis-memory", "")                    // older praxis: no source
	legacyInstall(t, claude, "use-ig", "embedded")                   // recorded source
	orgSkill := legacyInstall(t, claude, "praxis-onboarding", "cli") // an org skill reusing the name
	legacyInstall(t, codex, "praxis-getting-started", "")
	legacyInstall(t, gemini, "praxis-getting-started", "") // same folder, second entry
	untouched := legacyInstall(t, noPraxis, "praxis-memory", "")

	retired, err := RetireLegacyBuiltins([]harness.Harness{claude, codex, gemini, noPraxis})
	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 4 {
		t.Errorf("retired %d entries, want 4: %+v", len(retired), retired)
	}
	for _, gone := range []string{
		filepath.Join(claude.SkillDir, "praxis-memory"),
		filepath.Join(claude.SkillDir, "use-ig"),
		filepath.Join(codex.SkillDir, "praxis-getting-started"),
	} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s still discoverable: %v", gone, err)
		}
	}
	for _, kept := range []Installation{orgSkill, untouched} {
		if _, err := os.Stat(kept.Path); err != nil {
			t.Errorf("%s removed: %v", kept.Path, err)
		}
	}
	left, _ := List()
	var names []string
	for _, e := range left {
		if e.SkillName != "praxis" {
			names = append(names, e.Harness+":"+e.SkillName)
		}
	}
	slices.Sort(names)
	if want := []string{"antigravity:praxis-memory", "claude-code:praxis-onboarding"}; !slices.Equal(names, want) {
		t.Errorf("receipt keeps %v, want %v (both shared-root entries must go)", names, want)
	}
	found := 0
	_ = filepath.WalkDir(filepath.Join(os.Getenv("HOME"), ".praxis", "backups"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "SKILL.md" {
			found++
		}
		return nil
	})
	if found != 2 { // the two entries with no recorded source and digest
		t.Errorf("backups = %d, want 2", found)
	}
}

// Refresh (after `praxis update`) also retires the replaced skills.
func TestRefresh_RetiresLegacyBuiltins(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)[:1]
	if _, err := Install("praxis", hosts); err != nil {
		t.Fatal(err)
	}
	legacyInstall(t, hosts[0], "praxis-memory", "")
	if _, err := Refresh(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(hosts[0].SkillDir, "praxis-memory")); !os.IsNotExist(err) {
		t.Errorf("praxis-memory survived Refresh: %v", err)
	}
}

// The digest RefreshIfStale expects is the digest a real install records.
func TestEmbeddedDigestMatchesInstall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	results, err := Install("praxis", fakeHosts(t))
	if err != nil {
		t.Fatal(err)
	}
	want, err := embeddedDigest("praxis")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Digest != want {
			t.Errorf("%s: installed digest %s, embeddedDigest %s", r.Harness, r.Digest, want)
		}
	}
}

func setReceiptDigests(t *testing.T, digest string) {
	t.Helper()
	r, err := loadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	for i := range r.Skills {
		if r.Skills[i].SkillName == "praxis" {
			r.Skills[i].Digest = digest
		}
	}
	if err := saveReceipt(r); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshIfStale(t *testing.T) {
	t.Run("nothing installed: first run's job", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if got, err := RefreshIfStale(); err != nil || got != nil {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("current install: no rewrite, an edit is kept", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		hosts := fakeHosts(t)[:1]
		results, err := Install("praxis", hosts)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(results[0].Path, []byte("my edit"), 0600); err != nil {
			t.Fatal(err)
		}
		if got, err := RefreshIfStale(); err != nil || got != nil {
			t.Fatalf("refreshed a current install: %v, %v", got, err)
		}
		if b, _ := os.ReadFile(results[0].Path); string(b) != "my edit" {
			t.Error("the user's edit was overwritten")
		}
	})
	t.Run("installed by another binary: rewritten once", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		hosts := fakeHosts(t)[:1]
		results, err := Install("praxis", hosts)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(results[0].Path, []byte("old text"), 0600); err != nil {
			t.Fatal(err)
		}
		d, _ := digestTree(filepath.Dir(results[0].Path))
		setReceiptDigests(t, d) // what the older binary recorded
		got, err := RefreshIfStale()
		if err != nil || len(got) != 1 {
			t.Fatalf("got %v, %v", got, err)
		}
		want, _ := ContentFor("praxis")
		if b, _ := os.ReadFile(results[0].Path); string(b) != want {
			t.Error("SKILL.md not rewritten to this binary's text")
		}
		if again, err := RefreshIfStale(); err != nil || again != nil {
			t.Errorf("second call refreshed again: %v, %v", again, err)
		}
	})
	t.Run("receipt from a praxis without digests: refresh and retire", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		hosts := fakeHosts(t)[:1]
		if _, err := Install("praxis", hosts); err != nil {
			t.Fatal(err)
		}
		setReceiptDigests(t, "")
		legacyInstall(t, hosts[0], "praxis-memory", "")
		if _, err := RefreshIfStale(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(hosts[0].SkillDir, "praxis-memory")); !os.IsNotExist(err) {
			t.Errorf("praxis-memory survived: %v", err)
		}
	})
}

// Only stale installs are rewritten: a current install on another host keeps
// the user's edit active.
func TestRefreshIfStale_OnlyStaleEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)[:2]
	results, err := Install("praxis", hosts)
	if err != nil {
		t.Fatal(err)
	}
	stale, current := results[0], results[1]
	r, err := loadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	for i := range r.Skills {
		if r.Skills[i].Path == stale.Path {
			r.Skills[i].Digest = "written-by-an-older-binary"
		}
	}
	if err := saveReceipt(r); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current.Path, []byte("my edit"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := RefreshIfStale()
	if err != nil || len(got) != 1 || got[0].Path != stale.Path {
		t.Fatalf("refreshed %+v, %v; want only %s", got, err, stale.Path)
	}
	if b, _ := os.ReadFile(current.Path); string(b) != "my edit" {
		t.Error("a current install was rewritten")
	}
}
