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

// A linked skill folder is usually the user's own checkout: never replace it
// or write through it.
func TestLifecycle_LinkedSkillFolderNeverFollowed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)[:1]
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "SKILL.md"), []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(hosts[0].SkillDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(hosts[0].SkillDir, "praxis-test")); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallWithBody("praxis-test", "replacement", hosts); err == nil {
		t.Error("install over a linked skill folder must fail closed")
	}
	if got := string(readBytes(t, filepath.Join(external, "SKILL.md"))); got != "external" {
		t.Errorf("link target changed: %q", got)
	}
}

// A skill root, or a folder above it, linked into a dotfiles repo is a normal
// setup: installs land in the link target and the receipt keeps the path the
// host reads.
func TestLifecycle_LinkedRootsAreUsed(t *testing.T) {
	for _, tc := range []struct{ name, link string }{
		{"linked skill root", "claude/skills"},
		{"linked parent", "claude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			base := t.TempDir()
			dotfiles := t.TempDir()
			link := filepath.Join(base, tc.link)
			if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(dotfiles, link); err != nil {
				t.Fatal(err)
			}
			hosts := []harness.Harness{{Name: "claude-code", SkillDir: filepath.Join(base, "claude", "skills")}}
			// The activating rename must start beside the real root: a linked
			// root can be on another filesystem, where a rename cannot cross.
			var staged []string
			original := renameTree
			renameTree = func(a, b string) error {
				if strings.HasSuffix(a, string(filepath.Separator)+"new") {
					staged = append(staged, a)
				}
				return original(a, b)
			}
			t.Cleanup(func() { renameTree = original })

			for _, body := range []string{"v1", "v2"} {
				got, err := InstallWithBody("praxis-test", body, hosts)
				if err != nil {
					t.Fatalf("install %s: %v", body, err)
				}
				if want := filepath.Join(hosts[0].SkillDir, "praxis-test", "SKILL.md"); got[0].Path != want {
					t.Errorf("receipt path = %q, want the host's path %q", got[0].Path, want)
				}
			}
			real, err := filepath.EvalSymlinks(filepath.Join(hosts[0].SkillDir, "praxis-test", "SKILL.md"))
			if err != nil || !strings.HasPrefix(real, mustEval(t, dotfiles)) {
				t.Fatalf("skill not in the link target: %q, %v", real, err)
			}
			realRoot := filepath.Dir(filepath.Dir(real))
			for _, a := range staged {
				if filepath.Dir(filepath.Dir(a)) != filepath.Dir(realRoot) {
					t.Errorf("staged at %s, want beside the real root %s", a, realRoot)
				}
			}
			if len(staged) != 2 {
				t.Errorf("activations = %v, want 2", staged)
			}
			if s := string(readBytes(t, real)); s != "v2" {
				t.Errorf("SKILL.md = %q", s)
			}
			if b := backups(t); len(b) != 0 {
				t.Errorf("owned, unmodified content was backed up: %v", b)
			}
			for _, dir := range []string{base, filepath.Dir(dotfiles), dotfiles} {
				if left, _ := filepath.Glob(filepath.Join(dir, "*", ".praxis-stage-*")); len(left) > 0 {
					t.Errorf("staging left behind: %v", left)
				}
				if left, _ := filepath.Glob(filepath.Join(dir, ".praxis-stage-*")); len(left) > 0 {
					t.Errorf("staging left behind: %v", left)
				}
			}
			if _, err := Uninstall("praxis-test"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(real); !os.IsNotExist(err) {
				t.Errorf("uninstall left %s: %v", real, err)
			}
		})
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
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

// backups lists every file under the state root's backups directory.
func backups(t *testing.T) map[string]string {
	t.Helper()
	root, _ := paths.ActiveRoot()
	got := map[string]string{}
	_ = filepath.WalkDir(filepath.Join(root, "backups"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Type()&fs.ModeSymlink == 0 {
			got[p] = string(readBytes(t, p))
		}
		return nil
	})
	return got
}

// A receipt written by an older praxis has no source or digest, so the first
// rewrite cannot prove the files are unchanged: it backs them up once. From
// then on the digest proves ownership and no further backup is made.
func TestLifecycle_LegacyReceiptBacksUpOnceThenTrustsDigest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)[:1]
	dir := filepath.Join(hosts[0].SkillDir, "praxis-test")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("from old praxis"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveReceipt(Receipt{Skills: []Installation{{SkillName: "praxis-test", Harness: "claude-code", Path: filepath.Join(dir, "SKILL.md")}}}); err != nil {
		t.Fatal(err)
	}

	got, err := InstallWithBody("praxis-test", "v2", hosts)
	if err != nil || len(got) != 1 || got[0].Digest == "" || got[0].Source == "" {
		t.Fatalf("install = %+v, %v", got, err)
	}
	b := backups(t)
	if len(b) != 2 { // content/SKILL.md + provenance.json
		t.Fatalf("legacy content backups = %v, want one copy", b)
	}
	if _, err := InstallWithBody("praxis-test", "v3", hosts); err != nil {
		t.Fatal(err)
	}
	if after := backups(t); len(after) != len(b) {
		t.Errorf("owned, unmodified content was backed up again: %v", after)
	}
	if s := string(readBytes(t, filepath.Join(dir, "SKILL.md"))); s != "v3" {
		t.Errorf("SKILL.md = %q", s)
	}
}

// Retirement removes a package from discovery and the receipt, backs up
// content the user changed, and never acts outside the selected host roots.
func TestLifecycle_Retire(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)[:2]
	if _, err := InstallWithBody("praxis-old", "old", hosts); err != nil {
		t.Fatal(err)
	}
	edited := filepath.Join(hosts[1].SkillDir, "praxis-old", "SKILL.md")
	if err := os.WriteFile(edited, []byte("user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	receipt, err := loadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	outside := Installation{SkillName: "praxis-old", Harness: "other", Path: filepath.Join(t.TempDir(), "praxis-old", "SKILL.md")}
	if err := os.MkdirAll(filepath.Dir(outside.Path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside.Path, []byte("not ours"), 0600); err != nil {
		t.Fatal(err)
	}

	err = withSkillLock(func() error {
		_, err := applyPackages(nil, append(receipt.Skills, outside), hosts)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hosts {
		if _, err := os.Stat(filepath.Join(h.SkillDir, "praxis-old")); !os.IsNotExist(err) {
			t.Errorf("%s still discoverable: %v", h.Name, err)
		}
	}
	if left, _ := List(); len(left) != 0 {
		t.Errorf("receipt still lists %v", left)
	}
	if string(readBytes(t, outside.Path)) != "not ours" {
		t.Error("retired a package outside the selected hosts")
	}
	found := false
	for _, body := range backups(t) {
		found = found || body == "user edit"
	}
	if !found {
		t.Error("the user's edit was retired without a backup")
	}
}

// Scripts become executable; other files keep at most their source exec bit.
func TestLifecycle_FileModes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)[:1]
	tree := fstest.MapFS{
		"SKILL.md":        &fstest.MapFile{Data: []byte("x"), Mode: 0644},
		"scripts/run.sh":  &fstest.MapFile{Data: []byte("#!/bin/sh"), Mode: 0644},
		"tools/helper.sh": &fstest.MapFile{Data: []byte("#!/bin/sh"), Mode: 0755},
		"references/a.md": &fstest.MapFile{Data: []byte("a"), Mode: 0644},
	}
	if _, err := InstallTree("praxis-test", tree, hosts); err != nil {
		t.Fatal(err)
	}
	want := map[string]fs.FileMode{"SKILL.md": 0600, "scripts/run.sh": 0700, "tools/helper.sh": 0700, "references/a.md": 0600}
	for p, mode := range want {
		fi, err := os.Stat(filepath.Join(hosts[0].SkillDir, "praxis-test", p))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != mode {
			t.Errorf("%s mode = %o, want %o", p, fi.Mode().Perm(), mode)
		}
	}
	if _, err := InstallTreeWithBodies("praxis-test", "y", []FileBody{{"scripts/x.py", "print(1)"}}, hosts); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(hosts[0].SkillDir, "praxis-test", "scripts", "x.py")); fi.Mode().Perm() != 0700 {
		t.Errorf("server script mode = %o, want 700", fi.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(hosts[0].SkillDir, "praxis-test", "tools")); !os.IsNotExist(err) {
		t.Error("a replaced tree kept a file the new tree does not have")
	}
}

// A link inside a modified skill is backed up as a link, never followed.
func TestLifecycle_BackupKeepsLinksAsLinks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)[:1]
	if _, err := InstallWithBody("praxis-test", "old", hosts); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("do not copy"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(hosts[0].SkillDir, "praxis-test", "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallWithBody("praxis-test", "new", hosts); err != nil {
		t.Fatal(err)
	}
	for p, body := range backups(t) {
		if body == "do not copy" {
			t.Errorf("backup followed the link into %s", p)
		}
	}
	root, _ := paths.ActiveRoot()
	links, _ := filepath.Glob(filepath.Join(root, "backups", "*", "content", "link"))
	if len(links) != 1 {
		t.Fatalf("link not preserved in backup: %v", links)
	}
	if target, _ := os.Readlink(links[0]); target != secret {
		t.Errorf("backup link target = %q", target)
	}
}
