package skillinstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
)

func exporterFixture(t *testing.T, source string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "raptor")
	script := "#!/bin/sh\nset -eu\n[ \"$1 $2 $3 $4\" = 'install-skills --agent claude --path' ]\n[ \"$HOME\" = \"$5\" ]\n[ -z \"${FACETS_TOKEN:-}\" ]\n/bin/mkdir -p \"$5/.claude/skills\"\n/bin/cp -R '" + source + "' \"$5/.claude/skills/raptor\"\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestInstallRaptorCompleteTreeAndSharedTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FACETS_TOKEN", "must-not-reach-child")
	source := filepath.Join(t.TempDir(), "source")
	if err := writeTree(packageFixture("raptor"), source); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(home, "repo/.agents/skills")
	hosts := []harness.Harness{{Name: "codex", SkillDir: shared}, {Name: "gemini-cli", SkillDir: shared}, {Name: "claude-code", SkillDir: filepath.Join(home, "repo/.claude/skills")}}
	installed, err := InstallRaptor(exporterFixture(t, source), hosts)
	if err != nil || len(installed) != 3 {
		t.Fatalf("installed=%v err=%v", installed, err)
	}
	for _, e := range installed {
		if e.Source != "raptor" || e.Scope != "builtin" || e.Digest == "" {
			t.Errorf("missing provenance: %+v", e)
		}
		if err := verifyPackage(filepath.Dir(e.Path), "raptor"); err != nil {
			t.Fatal(err)
		}
		if body, err := os.ReadFile(filepath.Join(filepath.Dir(e.Path), "assets/.hidden")); err != nil || string(body) != "asset" {
			t.Fatalf("hidden asset lost: %s %v", body, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".agents/skills/raptor")); !os.IsNotExist(err) {
		t.Error("project install wrote global tree")
	}
	if _, err := os.Stat(filepath.Join(home, ".facets")); !os.IsNotExist(err) {
		t.Error("export changed real Raptor state")
	}
	// A second install must not need the CLI, overwrite sources, or duplicate receipts.
	again, err := InstallRaptor("/missing/binary", hosts)
	if err != nil || len(again) != 0 {
		t.Fatalf("existing package re-exported: %v %v", again, err)
	}
	r, err := List()
	if err != nil || len(r) != 3 {
		t.Fatalf("receipt=%v %v", r, err)
	}
}

func TestInstallRaptorPreservesExistingLocalSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	source := filepath.Join(t.TempDir(), "source")
	if err := writeTree(packageFixture("raptor"), source); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(home, ".codex/skills")
	if err := os.MkdirAll(native, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(native, "raptor")); err != nil {
		t.Fatal(err)
	}
	hosts := []harness.Harness{{Name: "codex", SkillDir: filepath.Join(home, ".agents/skills")}}
	if result, err := InstallRaptor("", hosts); err != nil || len(result) != 0 {
		t.Fatalf("existing source=%v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(hosts[0].SkillDir, "raptor")); !os.IsNotExist(err) {
		t.Error("duplicated native package in shared root")
	}
	if link, _ := os.Readlink(filepath.Join(native, "raptor")); link != source {
		t.Error("source symlink changed")
	}
	// Broken source cannot be papered over by installing a shadowing copy.
	if err := os.Remove(filepath.Join(source, "references/route.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallRaptor("", hosts); err == nil || !strings.Contains(err.Error(), "preserving invalid") {
		t.Fatalf("invalid source=%v", err)
	}
}

func TestRaptorExportStopsCredentialWalkInsideNestedTMPDIR(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(home, "project")
	temp := filepath.Join(project, "tmp")
	if err := os.MkdirAll(filepath.Join(project, ".facets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(temp, 0700); err != nil {
		t.Fatal(err)
	}
	credentials := filepath.Join(project, ".facets/credentials")
	if err := os.WriteFile(credentials, []byte("unrelated-invalid-profile"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temp)
	source := filepath.Join(t.TempDir(), "source")
	if err := writeTree(packageFixture("raptor"), source); err != nil {
		t.Fatal(err)
	}
	bin := exporterFixture(t, source)
	body := readBytes(t, bin)
	// Simulate Raptor's cwd-parent walk; the first credentials file must be
	// the empty fence in its disposable HOME, never the user's parent profile.
	walk := "dir=$PWD\nwhile [ \"$dir\" != / ]; do\n if [ -f \"$dir/.facets/credentials\" ]; then\n  [ \"$dir/.facets/credentials\" -ef \"$HOME/.facets/credentials\" ] || exit 17\n  [ ! -s \"$dir/.facets/credentials\" ] || exit 18\n  break\n fi\n dir=$(/usr/bin/dirname \"$dir\")\ndone\n"
	if err := os.WriteFile(bin, []byte(strings.Replace(string(body), "set -eu\n", "set -eu\n"+walk, 1)), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallRaptor(bin, fakeHosts(t)[:1]); err != nil {
		t.Fatalf("parent credentials affected export: %v", err)
	}
	if got := readBytes(t, credentials); string(got) != "unrelated-invalid-profile" {
		t.Fatal("parent credentials changed")
	}
}

func TestInstallRaptorRejectsIncompleteExportsWithoutHostWrites(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(string)
		binary string
		want   string
	}{
		{"old-bundle", func(p string) { os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("# Legacy skill"), 0600) }, "", "consolidated skill"},
		{"missing-reference", func(p string) { os.Remove(filepath.Join(p, "scripts/helper.sh")) }, "", "consolidated skill"},
		{"missing-cli", nil, "", "missing"},
		{"failed-command", nil, "/does-not-exist/raptor", "export failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			source := filepath.Join(t.TempDir(), "source")
			if err := writeTree(packageFixture("raptor"), source); err != nil {
				t.Fatal(err)
			}
			binary := tc.binary
			if tc.setup != nil {
				tc.setup(source)
				binary = exporterFixture(t, source)
			}
			hosts := fakeHosts(t)[:1]
			if _, err := InstallRaptor(binary, hosts); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want=%s", err, tc.want)
			}
			if _, err := os.Lstat(filepath.Join(hosts[0].SkillDir, "raptor")); !os.IsNotExist(err) {
				t.Fatal("invalid export reached host")
			}
		})
	}
}

func TestNewOnlyPackagePreservesCreationDuringStaging(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)[:1]
	dest := filepath.Join(hosts[0].SkillDir, "raptor")
	w := packageWrite{name: "raptor", source: "raptor", mustBeAbsent: true, write: func(stage string) error {
		if err := writeTree(packageFixture("raptor"), dest); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dest, "SKILL.md"), []byte("independent creation"), 0600); err != nil {
			return err
		}
		return writeTree(packageFixture("raptor"), stage)
	}}
	_, err := applyPackages([]packageWrite{w}, nil, hosts)
	if err == nil {
		t.Error("new-only package overwrote a concurrent creation")
	}
	if body := readBytes(t, filepath.Join(dest, "SKILL.md")); string(body) != "independent creation" {
		t.Errorf("independent content replaced: %s", body)
	}
}

func TestRaptorInstallNeverReplacesConcurrentEmptyDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	source := filepath.Join(t.TempDir(), "source")
	if err := writeTree(packageFixture("raptor"), source); err != nil {
		t.Fatal(err)
	}
	hosts := fakeHosts(t)[:1]
	dest := filepath.Join(hosts[0].SkillDir, "raptor")
	var created os.FileInfo
	original := renameNewTree
	renameNewTree = func(from, to string) error {
		if to == dest {
			if err := os.Mkdir(dest, 0700); err != nil {
				return err
			}
			created, _ = os.Stat(dest)
		}
		return original(from, to)
	}
	t.Cleanup(func() { renameNewTree = original })
	if _, err := InstallRaptor(exporterFixture(t, source), hosts); err == nil {
		t.Fatal("concurrent empty directory was overwritten")
	}
	after, err := os.Stat(dest)
	if err != nil || created == nil || !os.SameFile(created, after) {
		t.Fatal("concurrent directory was removed or replaced")
	}
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("activation wrote into independent directory")
	}
}

func TestRaptorMixedNativeAndSharedHostsDoNotCreateDuplicates(t *testing.T) {
	for _, tc := range []struct {
		name, existing, missing string
		project, onlyMissing    bool
	}{
		{"gemini-native", "gemini-cli", "codex", false, false},
		{"codex-native", "codex", "gemini-cli", false, false},
		{"only-codex-targeted", "gemini-cli", "codex", false, true},
		{"project-with-inherited-gemini", "gemini-cli", "codex", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			source := filepath.Join(t.TempDir(), "source")
			if err := writeTree(packageFixture("raptor"), source); err != nil {
				t.Fatal(err)
			}
			native := map[string]string{"codex": ".codex/skills", "gemini-cli": ".gemini/skills"}
			if err := writeTree(packageFixture("raptor"), filepath.Join(home, native[tc.existing], "raptor")); err != nil {
				t.Fatal(err)
			}
			base := home
			if tc.project {
				base = filepath.Join(home, "project")
			}
			shared := filepath.Join(base, ".agents/skills")
			hosts := []harness.Harness{{Name: tc.missing, SkillDir: shared}}
			if !tc.onlyMissing {
				hosts = append(hosts, harness.Harness{Name: tc.existing, SkillDir: shared})
			}
			installed, err := InstallRaptor(exporterFixture(t, source), hosts)
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(base, native[tc.missing], "raptor/SKILL.md")
			if len(installed) != 1 || installed[0].Path != want {
				t.Fatalf("install=%v, want missing host's native path %s", installed, want)
			}
			if _, err := os.Stat(filepath.Join(shared, "raptor")); !os.IsNotExist(err) {
				t.Error("shared package duplicates the peer's native copy")
			}
			for _, h := range hosts {
				if !verifiedRaptor(h) {
					t.Errorf("original host discovery cannot find its installed package: %+v", h)
				}
			}
		})
	}
}
