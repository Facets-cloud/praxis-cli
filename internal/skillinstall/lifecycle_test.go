package skillinstall

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

func readBytes(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLifecycle_CanonicalDefaultInstallsCompleteEmbeddedTree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)[:1]
	if names := MetaSkillNames(); len(names) != 1 || names[0] != "praxis" {
		t.Errorf("default entrypoints = %v, want only praxis", names)
	}
	for _, name := range BootstrapSkillNames() {
		if _, err := Install(name, hosts); err != nil {
			t.Fatal(err)
		}
	}
	source := "embedded/praxis"
	err := filepath.WalkDir(source, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(source, p)
		got, err := os.ReadFile(filepath.Join(hosts[0].SkillDir, "praxis", rel))
		if err != nil {
			t.Errorf("complete file missing %s: %v", rel, err)
			return nil
		}
		if !bytes.Equal(readBytes(t, p), got) {
			t.Errorf("embedded bytes differ: %s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLifecycle_RollbackAfterActivationAndReceiptFailure(t *testing.T) {
	for _, failReceipt := range []bool{false, true} {
		t.Run(fmt.Sprint(failReceipt), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			hosts := fakeHosts(t)[:2]
			if _, err := InstallWithBody("praxis-test", "old", hosts); err != nil {
				t.Fatal(err)
			}
			receipt, _ := paths.Installed()
			before := readBytes(t, receipt)
			original := renameTree
			calls := 0
			renameTree = func(a, b string) error {
				calls++
				if !failReceipt && calls == 4 {
					return fmt.Errorf("injected activation failure")
				}
				if failReceipt && calls == 4 {
					// The destination becomes unwritable only after every stage
					// was prepared; restoring the trees must still succeed.
					if err := os.Rename(receipt, receipt+".saved"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(receipt, 0700); err != nil {
						t.Fatal(err)
					}
				}
				return original(a, b)
			}
			t.Cleanup(func() { renameTree = original })
			got, err := InstallWithBody("praxis-test", "new", hosts)
			if err == nil || len(got) != 0 {
				t.Fatalf("wanted rollback, got %v %v", got, err)
			}
			for _, h := range hosts {
				if string(readBytes(t, filepath.Join(h.SkillDir, "praxis-test", "SKILL.md"))) != "old" {
					t.Error("old package not restored")
				}
			}
			if !failReceipt && !bytes.Equal(before, readBytes(t, receipt)) {
				t.Error("receipt changed on failure")
			}
		})
	}
}

func TestLifecycle_MissingTreeEntrypointAndDuplicatePathsPreserveOld(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)[:1]
	if _, err := InstallWithBody("praxis-test", "old", hosts); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallTree("praxis-test", fstest.MapFS{"refs/a.md": &fstest.MapFile{Data: []byte("a")}}, hosts); err == nil {
		t.Error("missing SKILL.md accepted")
	}
	if _, err := InstallTreeWithBodies("praxis-test", "new", []FileBody{{"refs/a", "one"}, {"refs/A", "two"}}, hosts); err == nil {
		t.Error("case-colliding paths accepted")
	}
	if got := string(readBytes(t, filepath.Join(hosts[0].SkillDir, "praxis-test", "SKILL.md"))); got != "old" {
		t.Errorf("lost prior content: %s", got)
	}
}

func TestLifecycle_InvalidPathsPreserveTreeAndReceipt(t *testing.T) {
	for _, p := range []string{"../escape", "/absolute", `C:\escape`, `refs\..\escape`, "./alias", "refs/../alias", "SKILL.md", "refs//alias", "NUL", "refs/file.", "a:b"} {
		t.Run(p, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			hosts := fakeHosts(t)[:1]
			if _, err := InstallTreeWithBodies("praxis-test", "old", []FileBody{{"refs/old", "support"}}, hosts); err != nil {
				t.Fatal(err)
			}
			receipt, _ := paths.Installed()
			before := readBytes(t, receipt)
			_, err := InstallTreeWithBodies("praxis-test", "new", []FileBody{{p, "bad"}}, hosts)
			if err == nil {
				t.Fatalf("unsafe path %q accepted", p)
			}
			if !strings.Contains(err.Error(), "path") {
				t.Fatalf("wrong error: %v", err)
			}
			if got := string(readBytes(t, filepath.Join(hosts[0].SkillDir, "praxis-test", "SKILL.md"))); got != "old" {
				t.Errorf("last usable skill destroyed: %q", got)
			}
			if !bytes.Equal(before, readBytes(t, receipt)) {
				t.Error("receipt changed after validation failure")
			}
		})
	}
}

func TestLifecycle_StageAllHostsBeforeReplacement(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)[:2]
	if _, err := InstallWithBody("praxis-test", "old", hosts[:1]); err != nil {
		t.Fatal(err)
	}
	receipt, _ := paths.Installed()
	before := readBytes(t, receipt)
	block := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(block, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	hosts[1].SkillDir = filepath.Join(block, "skills")
	if _, err := InstallWithBody("praxis-test", "new", hosts); err == nil {
		t.Fatal("wanted staging failure")
	}
	if got := string(readBytes(t, filepath.Join(hosts[0].SkillDir, "praxis-test", "SKILL.md"))); got != "old" {
		t.Errorf("partial install destroyed old content: %q", got)
	}
	if !bytes.Equal(before, readBytes(t, receipt)) {
		t.Error("receipt changed after staging failure")
	}
}

func TestLifecycle_SymlinksNeverFollowed(t *testing.T) {
	for _, linkRoot := range []bool{false, true} {
		t.Run(fmt.Sprint(linkRoot), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			hosts := fakeHosts(t)[:1]
			external := t.TempDir()
			if err := os.WriteFile(filepath.Join(external, "SKILL.md"), []byte("external"), 0600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(hosts[0].SkillDir, "praxis-test")
			if linkRoot {
				link = hosts[0].SkillDir
			}
			if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(external, link); err != nil {
				t.Fatal(err)
			}
			if _, err := InstallWithBody("praxis-test", "replacement", hosts); err == nil {
				t.Error("symlink install must fail closed")
			}
			if got := string(readBytes(t, filepath.Join(external, "SKILL.md"))); got != "external" {
				t.Errorf("symlink target changed: %q", got)
			}
		})
	}
}

func TestLifecycle_ModifiedContentBackedUpOutsideDiscovery(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)[:1]
	if _, err := InstallWithBody("praxis-test", "original", hosts); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(hosts[0].SkillDir, "praxis-test", "SKILL.md")
	if err := os.WriteFile(p, []byte("my edits"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallWithBody("praxis-test", "new", hosts); err != nil {
		t.Fatal(err)
	}
	root, _ := paths.ActiveRoot()
	found := false
	_ = filepath.WalkDir(filepath.Join(root, "backups"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			found = found || string(b) == "my edits"
		}
		return nil
	})
	if !found {
		t.Error("modified content lost: no recoverable backup under state root")
	}
}

func TestLifecycle_ConcurrentInstallsPreserveEveryReceiptEntry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)[:1]
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := InstallWithBody(fmt.Sprintf("praxis-test-%d", i), "body", hosts); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 12 {
		t.Errorf("lost concurrent receipt updates: got %d, want 12", len(got))
	}
}

func TestLifecycle_InvalidSkillNames(t *testing.T) {
	for _, name := range []string{"", "../escape", "x/y", `x\y`, "CON", "x.", "x:y"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if _, err := InstallWithBody(name, "body", []harness.Harness{{Name: "test", SkillDir: filepath.Join(t.TempDir(), "skills")}}); err == nil {
				t.Errorf("unsafe skill name %q accepted", name)
			}
		})
	}
}

func TestLifecycle_RefreshReplacesOldBuiltinsWithoutReinstallingThem(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixtureCanonical(t)
	hosts := fakeHosts(t)[:1]
	if _, err := Install("praxis-memory", hosts); err != nil {
		t.Fatal(err)
	}
	// Use the actual supported target discovery for this no-argument API.
	hosts[0].SkillDir = filepath.Join(os.Getenv("HOME"), ".claude", "skills")
	if _, err := Install("praxis-memory", hosts); err != nil {
		t.Fatal(err)
	}
	if _, err := Refresh(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(hosts[0].SkillDir, "praxis", "scripts/helper.sh")); err != nil {
		t.Errorf("offline replacement missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hosts[0].SkillDir, "praxis-memory")); !os.IsNotExist(err) {
		t.Errorf("old builtin remains discoverable: %v", err)
	}
}

func TestLifecycle_RollbackDoesNotMoveForeignReplacement(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)[:2]
	if _, err := InstallWithBody("praxis-test", "old", hosts); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(hosts[0].SkillDir, "praxis-test")
	aside := filepath.Join(t.TempDir(), "activated")
	original := renameTree
	calls := 0
	renameTree = func(a, b string) error {
		calls++
		if calls == 4 {
			if err := os.Rename(first, aside); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(first, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(first, "mine"), []byte("foreign"), 0600); err != nil {
				t.Fatal(err)
			}
			return fmt.Errorf("injected failure after external replacement")
		}
		return original(a, b)
	}
	t.Cleanup(func() { renameTree = original })
	_, err := InstallWithBody("praxis-test", "new", hosts)
	if err == nil {
		t.Fatal("wanted rollback failure")
	}
	if b, e := os.ReadFile(filepath.Join(first, "mine")); e != nil || string(b) != "foreign" {
		t.Errorf("rollback moved foreign directory: %q %v", b, e)
	}
	if !strings.Contains(err.Error(), "recover") {
		t.Errorf("must report retained recovery path: %v", err)
	}
}

func TestLifecycle_ChangedAfterStagingAbortsWithoutLosingEdits(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)[:2]
	if _, err := InstallWithBody("praxis-test", "old", hosts); err != nil {
		t.Fatal(err)
	}
	original := renameTree
	calls := 0
	renameTree = func(a, b string) error {
		calls++
		if calls == 2 {
			if err := os.WriteFile(filepath.Join(hosts[1].SkillDir, "praxis-test", "SKILL.md"), []byte("concurrent edit"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return original(a, b)
	}
	t.Cleanup(func() { renameTree = original })
	_, err := InstallWithBody("praxis-test", "new", hosts)
	if err == nil {
		t.Error("must abort when prior tree changes after staging")
	}
	if b := readBytes(t, filepath.Join(hosts[1].SkillDir, "praxis-test", "SKILL.md")); string(b) != "concurrent edit" {
		t.Errorf("lost concurrent edit: %s", b)
	}
}

func TestLifecycle_ConcurrentReceiptWriterCannotLoseAgentMetadata(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)[:1]
	if _, err := InstallWithBody("praxis-test", "old", hosts); err != nil {
		t.Fatal(err)
	}
	original := renameTree
	calls := 0
	renameTree = func(a, b string) error {
		calls++
		if calls == 2 {
			r, err := loadReceipt()
			if err != nil {
				t.Fatal(err)
			}
			r.Agents = append(r.Agents, AgentInstallation{AgentName: "concurrent-agent", Path: "preserve"})
			if err := saveReceipt(r); err != nil {
				t.Fatal(err)
			}
		}
		return original(a, b)
	}
	t.Cleanup(func() { renameTree = original })
	_, err := InstallWithBody("praxis-test", "new", hosts)
	if err == nil {
		t.Error("must abort on receipt modified by an independent writer")
	}
	r, err := loadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Agents) != 1 || r.Agents[0].AgentName != "concurrent-agent" {
		t.Errorf("lost concurrent agent receipt update: %v", r.Agents)
	}
	if b := readBytes(t, filepath.Join(hosts[0].SkillDir, "praxis-test", "SKILL.md")); string(b) != "old" {
		t.Error("skill tree not rolled back")
	}
}
