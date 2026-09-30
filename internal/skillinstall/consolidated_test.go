package skillinstall

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/skillcatalog"
)

func packageFixture(name string) fs.FS {
	return fstest.MapFS{
		"SKILL.md":            &fstest.MapFile{Data: []byte("---\nname: " + name + "\ndescription: Test package\nmetadata:\n  version: \"1.0\"\n---\nRead [route](references/route.md).\n")},
		"references/route.md": &fstest.MapFile{Data: []byte("Run [helper](../scripts/helper.sh).\n")},
		"scripts/helper.sh":   &fstest.MapFile{Data: []byte("#!/bin/sh\nexit 0\n"), Mode: 0700},
		"assets/.hidden":      &fstest.MapFile{Data: []byte("asset")},
	}
}

func fixtureCanonical(t *testing.T) {
	t.Helper()
	original := canonicalTree
	canonicalTree = func() fs.FS { return packageFixture("praxis") }
	t.Cleanup(func() { canonicalTree = original })
}

func TestConsolidated_CapabilitiesRequireLoadablePackagesOnEveryHost(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, []harness.Harness)
		want   []string
	}{
		{"both", nil, []string{"praxis-v1", "raptor-v1"}},
		{"missing-raptor", func(t *testing.T, h []harness.Harness) {
			if err := os.RemoveAll(filepath.Join(h[1].SkillDir, "raptor")); err != nil {
				t.Fatal(err)
			}
		}, []string{"praxis-v1"}},
		{"missing-nested-reference", func(t *testing.T, h []harness.Harness) {
			if err := os.Remove(filepath.Join(h[0].SkillDir, "raptor", "scripts/helper.sh")); err != nil {
				t.Fatal(err)
			}
		}, []string{"praxis-v1"}},
		{"forged-root-name", func(t *testing.T, h []harness.Harness) {
			p := filepath.Join(h[0].SkillDir, "raptor", "SKILL.md")
			b := readBytes(t, p)
			if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(b), "name: raptor", "name: bogus")), 0600); err != nil {
				t.Fatal(err)
			}
		}, []string{"praxis-v1"}},
		{"wrong-version", func(t *testing.T, h []harness.Harness) {
			p := filepath.Join(h[0].SkillDir, "raptor", "SKILL.md")
			b := readBytes(t, p)
			if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(b), "1.0", "0.1")), 0600); err != nil {
				t.Fatal(err)
			}
		}, []string{"praxis-v1"}},
		{"modified-praxis", func(t *testing.T, h []harness.Harness) {
			if err := os.WriteFile(filepath.Join(h[0].SkillDir, "praxis", "assets/.hidden"), []byte("modified"), 0600); err != nil {
				t.Fatal(err)
			}
		}, []string{"raptor-v1"}},
		{"raptor-root-symlink", func(t *testing.T, h []harness.Harness) {
			p := filepath.Join(h[0].SkillDir, "raptor")
			dst := filepath.Join(t.TempDir(), "package")
			if err := os.Rename(p, dst); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(dst, p); err != nil {
				t.Fatal(err)
			}
		}, []string{"praxis-v1", "raptor-v1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			fixtureCanonical(t)
			hosts := fakeHosts(t)[:2]
			if _, err := Install("praxis", hosts); err != nil {
				t.Fatal(err)
			}
			for _, h := range hosts {
				if err := writeTree(packageFixture("raptor"), filepath.Join(h.SkillDir, "raptor")); err != nil {
					t.Fatal(err)
				}
			}
			if tc.mutate != nil {
				tc.mutate(t, hosts)
			}
			if got := VerifiedCapabilities(hosts); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("capabilities = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConsolidated_RejectsInvalidRaptorFrontmatter(t *testing.T) {
	for _, header := range []string{
		"name: raptor\ndescription: test\nmetadata: invalid-scalar\n  version: \"1.0\"",
		"name: raptor\ndescription: [unclosed\nmetadata:\n  version: \"1.0\"",
		"name: raptor\ndescription: test\nmetadata:\n  version: \"1.0\"\nmetadata: invalid",
	} {
		dir := filepath.Join(t.TempDir(), "raptor")
		if err := writeTree(packageFixture("raptor"), dir); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\n"+header+"\n---\nRead [route](references/route.md).\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := verifyPackage(dir, "raptor"); err == nil {
			t.Errorf("unloadable frontmatter verified: %s", header)
		}
	}
}

func TestConsolidated_ArchivesAmbiguousTrackedBuiltinNamesake(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixtureCanonical(t)
	hosts := fakeHosts(t)[:1]
	if _, err := InstallWithBody("praxis-memory", "previous org memory policy", hosts); err != nil {
		t.Fatal(err)
	}
	r, _ := loadReceipt()
	for i := range r.Skills {
		r.Skills[i].Source = ""
		r.Skills[i].Scope = ""
		r.Skills[i].Digest = ""
	}
	if err := saveReceipt(r); err != nil {
		t.Fatal(err)
	}
	res := SyncCatalog(hosts, func([]string) ([]skillcatalog.Skill, error) { return nil, nil })
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if _, err := os.Stat(filepath.Join(hosts[0].SkillDir, "praxis-memory")); !os.IsNotExist(err) {
		t.Error("previous org memory policy remains discoverable")
	}
	found := false
	_ = filepath.WalkDir(RecoveryDirectory(), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			found = found || string(b) == "previous org memory policy"
		}
		return nil
	})
	if !found {
		t.Error("ambiguous old policy not archived")
	}
}

func TestConsolidated_SyncPreservesNamesakesMissingRaptorAndUntracked(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixtureCanonical(t)
	hosts := fakeHosts(t)[:1]
	seed := []skillcatalog.Skill{{Name: "cloud-operations", Scope: "global", Content: "old global"}, {Name: "release-debugging", Scope: "global", Content: "legacy Facets"}, {Name: "learning", Scope: "personal", Content: "my learning"}, {Name: "k8s-operations", Scope: "organization", Content: "our operations"}}
	if _, err := InstallCatalog(seed, hosts); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallWithBody("praxis-arbitrary", "user", hosts); err != nil {
		t.Fatal(err)
	}
	if _, err := Install("praxis-memory", hosts); err != nil {
		t.Fatal(err)
	}
	res := SyncCatalog(hosts, func(caps []string) ([]skillcatalog.Skill, error) {
		if !reflect.DeepEqual(caps, []string{"praxis-v1"}) {
			t.Errorf("missing Raptor advertised: %v", caps)
		}
		return seed, nil // a complete current bundle still contains these namesakes
	})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	for _, name := range []string{"praxis", "praxis-release-debugging", "praxis-learning", "praxis-k8s-operations", "praxis-arbitrary"} {
		if _, err := os.Stat(filepath.Join(hosts[0].SkillDir, name, "SKILL.md")); err != nil {
			t.Errorf("lost %s: %v", name, err)
		}
	}
	for _, name := range []string{"praxis-cloud-operations", "praxis-memory"} {
		if _, err := os.Stat(filepath.Join(hosts[0].SkillDir, name)); !os.IsNotExist(err) {
			t.Errorf("legacy %s not retired: %v", name, err)
		}
	}
}

func TestConsolidated_SuccessfulBundleArchivesPreviousOrganization(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixtureCanonical(t)
	hosts := fakeHosts(t)[:1]
	seed := []skillcatalog.Skill{{Name: "old-policy", Scope: "organization", Content: "previous organization policy"}, {Name: "release-debugging", Scope: "organization", Content: "previous organization release policy"}}
	if _, err := InstallCatalog(seed, hosts); err != nil {
		t.Fatal(err)
	}
	res := SyncCatalog(hosts, func([]string) ([]skillcatalog.Skill, error) {
		return []skillcatalog.Skill{{Name: "release-debugging", Scope: "global", Content: "current legacy raptor"}, {Name: "new-policy", Scope: "personal", Content: "current personal policy"}}, nil
	})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if _, err := os.Stat(filepath.Join(hosts[0].SkillDir, "praxis-old-policy")); !os.IsNotExist(err) {
		t.Errorf("stale organization policy remains active: %v", err)
	}
	if body := string(readBytes(t, filepath.Join(hosts[0].SkillDir, "praxis-release-debugging/SKILL.md"))); !strings.Contains(body, "current legacy raptor") {
		t.Errorf("stale namesake shadowed current catalog: %s", body)
	}
	root := filepath.Join(os.Getenv("HOME"), ".praxis/backups")
	found := map[string]bool{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			for _, sk := range seed {
				if strings.Contains(string(b), sk.Content) {
					found[sk.Name] = true
				}
			}
		}
		return nil
	})
	for _, sk := range seed {
		if !found[sk.Name] {
			t.Errorf("old organization content not recoverable: %s", sk.Name)
		}
	}
}

func TestConsolidated_FetchOrInstallFailureKeepsLastCatalog(t *testing.T) {
	for _, fetchFailure := range []bool{false, true} {
		t.Run(map[bool]string{true: "fetch", false: "install"}[fetchFailure], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			fixtureCanonical(t)
			hosts := fakeHosts(t)[:1]
			if _, err := InstallCatalog([]skillcatalog.Skill{{Name: "cloud-operations", Scope: "global", Content: "old"}}, hosts); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(hosts[0].SkillDir, "praxis-cloud-operations", "SKILL.md")
			before := readBytes(t, p)
			result := SyncCatalog(hosts, func([]string) ([]skillcatalog.Skill, error) {
				if fetchFailure {
					return nil, errors.New("offline")
				}
				return []skillcatalog.Skill{{Name: "valid", Content: "new"}, {Name: "invalid", Files: []skillcatalog.SkillFile{{Path: "../escape"}}}}, nil
			})
			if result.Err == nil {
				t.Fatal("wanted failure")
			}
			if string(readBytes(t, p)) != string(before) {
				t.Error("lost last catalog")
			}
			if _, err := os.Stat(filepath.Join(hosts[0].SkillDir, "praxis-valid")); !os.IsNotExist(err) {
				t.Error("partial catalog visible")
			}
		})
	}
}

func TestConsolidated_FailedPraxisInstallDoesNotAdvertise(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixtureCanonical(t)
	hosts := fakeHosts(t)[:1]
	external := t.TempDir()
	if err := os.MkdirAll(hosts[0].SkillDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(hosts[0].SkillDir, "praxis")); err != nil {
		t.Fatal(err)
	}
	res := SyncCatalog(hosts, func(caps []string) ([]skillcatalog.Skill, error) {
		if len(caps) != 0 {
			t.Errorf("advertised failed install: %v", caps)
		}
		return []skillcatalog.Skill{{Name: "cloud-operations", Scope: "global", Content: "fallback"}}, nil
	})
	if res.Err == nil {
		t.Error("missing canonical failure warning")
	}
	if _, err := os.Stat(filepath.Join(hosts[0].SkillDir, "praxis-cloud-operations", "SKILL.md")); err != nil {
		t.Errorf("lost fallback delivery: %v", err)
	}
}

func TestConsolidated_LegacyReceiptBuiltinMigrationPreservesAgentsAndOtherRoots(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixtureCanonical(t)
	hosts := fakeHosts(t)[:1]
	for _, name := range []string{"praxis-memory", "praxis-getting-started", "praxis-onboarding", "use-ig"} {
		if _, err := Install(name, hosts); err != nil {
			t.Fatal(err)
		}
	}
	other := fakeHosts(t)[:1]
	if _, err := Install("praxis-memory", other); err != nil {
		t.Fatal(err)
	}
	r, err := loadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	for i := range r.Skills {
		r.Skills[i].Source = ""
		r.Skills[i].Scope = ""
		r.Skills[i].Digest = ""
	}
	r.Agents = []AgentInstallation{{AgentName: "praxis-custom", Kind: "agent", Harness: "claude-code", Path: "untouched-agent"}}
	if err := saveReceipt(r); err != nil {
		t.Fatal(err)
	}
	if _, err := RefreshForHosts(hosts); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"praxis-memory", "praxis-getting-started", "praxis-onboarding", "use-ig"} {
		if _, err := os.Stat(filepath.Join(hosts[0].SkillDir, name)); !os.IsNotExist(err) {
			t.Errorf("known legacy builtin %s not migrated: %v", name, err)
		}
	}
	after, err := loadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after.Agents, r.Agents) {
		t.Error("agent receipt changed")
	}
	if _, err := os.Stat(filepath.Join(other[0].SkillDir, "praxis-memory", "SKILL.md")); err != nil {
		t.Error("other root changed")
	}
}

func TestConsolidated_EmbeddedPackageIsCompleteAndAdvertisable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hosts := fakeHosts(t)[:1]
	if _, err := Install("praxis", hosts); err != nil {
		t.Fatal(err)
	}
	if err := verifyPackage(filepath.Join(hosts[0].SkillDir, "praxis"), "praxis"); err != nil {
		t.Fatal(err)
	}
	if got := VerifiedCapabilities(hosts); !reflect.DeepEqual(got, []string{"praxis-v1"}) {
		t.Fatalf("full embedded package capabilities: %v", got)
	}
}

func TestConsolidated_RefreshSerializesFetchThroughCommit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixtureCanonical(t)
	hosts := fakeHosts(t)[:1]
	entered := make(chan struct{})
	release := make(chan struct{})
	secondFetch := make(chan struct{})
	done := make(chan SyncResult, 2)
	go func() {
		done <- SyncCatalog(hosts, func([]string) ([]skillcatalog.Skill, error) {
			close(entered)
			<-release
			return []skillcatalog.Skill{{Name: "one", Content: "one"}}, nil
		})
	}()
	<-entered
	go func() {
		done <- SyncCatalog(hosts, func([]string) ([]skillcatalog.Skill, error) {
			close(secondFetch)
			return []skillcatalog.Skill{{Name: "two", Content: "two"}}, nil
		})
	}()
	select {
	case <-secondFetch:
		t.Error("refresh entered fetch before prior commit")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	for i := 0; i < 2; i++ {
		if r := <-done; r.Err != nil {
			t.Fatal(r.Err)
		}
	}
	for _, name := range []string{"praxis-one", "praxis-two"} {
		if _, err := os.Stat(filepath.Join(hosts[0].SkillDir, name, "SKILL.md")); err != nil {
			t.Error(err)
		}
	}
}

func TestConsolidated_InvalidLocalRaptorDoesNotAdvertiseInheritedPackage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixtureCanonical(t)
	hosts := fakeHosts(t)[:1]
	global := filepath.Join(os.Getenv("HOME"), ".claude", "skills", "raptor")
	if err := writeTree(packageFixture("raptor"), global); err != nil {
		t.Fatal(err)
	}
	if err := writeTree(packageFixture("wrong-name"), filepath.Join(hosts[0].SkillDir, "raptor")); err != nil {
		t.Fatal(err)
	}
	if _, err := Install("praxis", hosts); err != nil {
		t.Fatal(err)
	}
	if got := VerifiedCapabilities(hosts); !reflect.DeepEqual(got, []string{"praxis-v1"}) {
		t.Errorf("unloadable local Raptor shadow advertised: %v", got)
	}
}

func TestConsolidated_OrganizationNamesakeOfOldBuiltinIsInstallable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixtureCanonical(t)
	hosts := fakeHosts(t)[:1]
	if _, err := Install("praxis-memory", hosts); err != nil {
		t.Fatal(err)
	}
	res := SyncCatalog(hosts, func([]string) ([]skillcatalog.Skill, error) {
		return []skillcatalog.Skill{{Name: "memory", Scope: "organization", Content: "organization memory policy"}}, nil
	})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	res = SyncCatalog(hosts, func([]string) ([]skillcatalog.Skill, error) {
		return []skillcatalog.Skill{{Name: "memory", Scope: "organization", Content: "organization memory policy"}}, nil
	})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	b := readBytes(t, filepath.Join(hosts[0].SkillDir, "praxis-memory", "SKILL.md"))
	if !strings.Contains(string(b), "organization memory policy") {
		t.Errorf("organization namesake was retired: %s", b)
	}
}

func TestConsolidated_SuccessfulBundleReconcilesPriorProfileWithBackups(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fixtureCanonical(t)
	hosts := fakeHosts(t)[:1]
	if _, err := InstallCatalog([]skillcatalog.Skill{{Name: "release-debugging", Scope: "organization", Content: "org A instructions"}, {Name: "revoked", Scope: "personal", Content: "revoked instructions"}}, hosts); err != nil {
		t.Fatal(err)
	}
	// Legacy tracked entries had no scope/digest metadata. They are uncertain,
	// not untracked: retain bytes outside discovery after a complete new bundle.
	if _, err := InstallWithBody("praxis-legacy-removed", "uncertain instructions", hosts); err != nil {
		t.Fatal(err)
	}
	r, err := loadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	for i := range r.Skills {
		if r.Skills[i].SkillName == "praxis-legacy-removed" {
			r.Skills[i].Source = ""
			r.Skills[i].Digest = ""
		}
	}
	if err := saveReceipt(r); err != nil {
		t.Fatal(err)
	}
	untouched := filepath.Join(hosts[0].SkillDir, "praxis-untracked")
	if err := os.MkdirAll(untouched, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(untouched, "SKILL.md"), []byte("untracked"), 0600); err != nil {
		t.Fatal(err)
	}
	res := SyncCatalog(hosts, func([]string) ([]skillcatalog.Skill, error) {
		return []skillcatalog.Skill{{Name: "release-debugging", Scope: "global", Content: "org B global instructions"}}, nil
	})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	b := readBytes(t, filepath.Join(hosts[0].SkillDir, "praxis-release-debugging", "SKILL.md"))
	if !strings.Contains(string(b), "org B global instructions") {
		t.Errorf("prior organization namesake stayed active: %s", b)
	}
	for _, name := range []string{"praxis-revoked", "praxis-legacy-removed"} {
		if _, err := os.Stat(filepath.Join(hosts[0].SkillDir, name)); !os.IsNotExist(err) {
			t.Errorf("obsolete tracked skill remains active: %s", name)
		}
	}
	if string(readBytes(t, filepath.Join(untouched, "SKILL.md"))) != "untracked" {
		t.Error("untracked directory touched")
	}
	backupRoot := filepath.Join(os.Getenv("HOME"), ".praxis", "backups")
	found := map[string]bool{}
	_ = filepath.WalkDir(backupRoot, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			for _, text := range []string{"org A instructions", "revoked instructions", "uncertain instructions"} {
				if strings.Contains(string(b), text) {
					found[text] = true
				}
			}
		}
		return nil
	})
	for _, text := range []string{"org A instructions", "revoked instructions", "uncertain instructions"} {
		if !found[text] {
			t.Errorf("no recovery copy for %s", text)
		}
	}
}
