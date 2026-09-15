package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/raptorinstall"
	"github.com/Facets-cloud/praxis-cli/internal/selfupdate"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
)

func useRealRaptorSetup(t *testing.T) {
	t.Helper()
	oldEnsure, oldInstall := ensureRaptorBinary, installRaptorSkills
	ensureRaptorBinary = raptorinstall.Ensure
	installRaptorSkills = runInstallRaptorSkills
	t.Cleanup(func() { ensureRaptorBinary, installRaptorSkills = oldEnsure, oldInstall })
}

func TestRefreshReportsRaptorFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := credentialsPut("default", "https://cp.invalid", "test", "key"); err != nil {
		t.Fatal(err)
	}
	old := postAuthSetup
	t.Cleanup(func() { postAuthSetup = old; refreshSkillsCmd.SetOut(nil) })
	postAuthSetup = func(io.Writer, bool, string, map[string]string) postAuthState {
		return postAuthState{raptorWarning: "incomplete Raptor skill"}
	}
	var out bytes.Buffer
	refreshSkillsCmd.SetOut(&out)
	if err := refreshSkillsCmd.RunE(refreshSkillsCmd, nil); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["skill_sync_complete"] != false || payload["raptor_warning"] != "incomplete Raptor skill" {
		t.Fatalf("partial refresh reported as complete: %s", out.String())
	}
}

func TestUpdateRunsRaptorUpgradeWhenPraxisIsCurrent(t *testing.T) {
	useRealRaptorSetup(t)
	oldUpdate := updateRaptor
	updateRaptor = runRaptorUpgrade
	t.Cleanup(func() { updateRaptor = oldUpdate })
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, "bin/raptor")
	mustMkdir(t, filepath.Dir(bin))
	t.Setenv("PATH", filepath.Dir(bin))
	marker := filepath.Join(home, "raptor-upgraded")
	script := "#!/bin/sh\n[ \"$*\" = 'upgrade --yes' ] || exit 9\n[ \"$RAPTOR_NO_SKILLS_UPGRADE\" = 1 ] || exit 8\nprintf done > '" + marker + "'\nprintf 'raptor progress\\n'\nprintf 'raptor stderr\\n' >&2\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	withFakeRelease(t, &selfupdate.Release{TagName: "v" + version}, nil)
	var out bytes.Buffer
	updateCmd.SetOut(&out)
	t.Cleanup(func() { updateCmd.SetOut(nil) })
	if err := updateCmd.RunE(updateCmd, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("Praxis was current but Raptor upgrade never ran: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("child output corrupted JSON: %s", out.String())
	}
	if payload["raptor"] == nil {
		t.Error("Raptor outcome omitted")
	}
}

func TestUpdateRaptorOutcomeAfterPraxisUpdateOrHomebrewDeferral(t *testing.T) {
	for _, tc := range []struct {
		name             string
		brew, failRaptor bool
	}{
		{"praxis-updated", false, false}, {"homebrew-deferred", true, false}, {"raptor-failed-after-praxis-update", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useRealRaptorSetup(t)
			oldUpdate := updateRaptor
			updateRaptor = runRaptorUpgrade
			t.Cleanup(func() { updateRaptor = oldUpdate })
			home := t.TempDir()
			t.Setenv("HOME", home)
			bin := filepath.Join(home, "bin/raptor")
			mustMkdir(t, filepath.Dir(bin))
			t.Setenv("PATH", filepath.Dir(bin))
			marker := filepath.Join(home, "upgrade-called")
			script := "#!/bin/sh\n[ \"$*\" = 'upgrade --yes' ] || exit 9\nprintf done > '" + marker + "'\nprintf 'upgrade output\\n'\n"
			if tc.failRaptor {
				script += "exit 6\n"
			}
			if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			self := filepath.Join(home, "praxis")
			if tc.brew {
				self = filepath.Join(home, "Caskroom/praxis/1/praxis")
			}
			mustMkdir(t, filepath.Dir(self))
			if err := os.WriteFile(self, []byte("old-praxis"), 0700); err != nil {
				t.Fatal(err)
			}
			withSelfPath(t, self)
			withFakeRelease(t, &selfupdate.Release{TagName: "v999.0.0", Assets: []selfupdate.Asset{{Name: "praxis_" + runtime.GOOS + "_" + runtime.GOARCH}}}, nil)
			oldDownload, oldRepair := downloadAsset, updateRepairHooks
			downloadAsset = func(string) (string, error) {
				p := filepath.Join(home, "new-praxis")
				return p, os.WriteFile(p, []byte("new-praxis"), 0700)
			}
			updateRepairHooks = func() ([]string, string) { return nil, "" }
			t.Cleanup(func() { downloadAsset, updateRepairHooks = oldDownload, oldRepair; updateCmd.SetOut(nil) })
			var out bytes.Buffer
			updateCmd.SetOut(&out)
			err := updateCmd.RunE(updateCmd, nil)
			if (err != nil) != tc.failRaptor {
				t.Fatalf("err=%v output=%s", err, out.String())
			}
			var payload struct {
				Updated bool                `json:"updated"`
				Reason  string              `json:"reason"`
				Raptor  raptorUpgradeResult `json:"raptor"`
			}
			if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
				t.Fatalf("invalid JSON: %s", out.String())
			}
			if !payload.Raptor.Attempted || payload.Raptor.Completed == tc.failRaptor || payload.Updated == tc.brew {
				t.Fatalf("wrong combined outcome: %+v", payload)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatal("Raptor never executed")
			}
			want := "new-praxis"
			if tc.brew {
				want = "old-praxis"
				if payload.Reason != "homebrew_managed" {
					t.Fatal("missing brew guidance")
				}
			}
			if got, _ := os.ReadFile(self); string(got) != want {
				t.Fatalf("Praxis install=%s want=%s", got, want)
			}
		})
	}
}

func TestRaptorUpgradeInteractiveAndMissingBinaryFailure(t *testing.T) {
	useRealRaptorSetup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, "bin/raptor")
	mustMkdir(t, filepath.Dir(bin))
	t.Setenv("PATH", filepath.Dir(bin))
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n[ \"$*\" = upgrade ] || exit 9\nprintf 'interactive upstream output\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	result, err := runRaptorUpgrade(&out, false, false)
	if err != nil || !result.Completed || !strings.Contains(out.String(), "interactive upstream output") {
		t.Fatalf("result=%+v err=%v output=%s", result, err, out.String())
	}
	ensureRaptorBinary = func() (raptorinstall.Result, error) {
		return raptorinstall.Result{}, errors.New("download unavailable")
	}
	result, err = runRaptorUpgrade(io.Discard, true, true)
	if err == nil || result.Attempted || !strings.Contains(result.Warning, "download unavailable") {
		t.Fatalf("failed install reported upgrade success: %+v %v", result, err)
	}
}

func TestSilentBootstrapDoesNotRunRaptor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	mustMkdir(t, filepath.Join(home, ".claude"))
	old := installRaptorSkills
	t.Cleanup(func() { installRaptorSkills = old })
	installRaptorSkills = func([]harness.Harness) ([]skillinstall.Installation, error) {
		t.Error("ordinary command bootstrap must not run Raptor")
		return nil, errors.New("no Raptor")
	}
	maybeFirstRunBootstrap([]string{"status"})
	if _, err := os.Stat(filepath.Join(home, ".claude/skills/praxis/SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestRaptorFailureLeavesLoginSuccessfulButSetupIncomplete(t *testing.T) {
	useRealRaptorSetup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	stubMCPManifestFetch(t)
	ensureRaptorBinary = func() (raptorinstall.Result, error) { return raptorinstall.Result{}, errors.New("checksum rejected") }
	var out bytes.Buffer
	state := runPostAuthSetup(&out, false, "https://cp.invalid", bearer("private"))
	payload := setupPayload("test", "test", "https://cp.invalid", "", false, state)
	if payload["ok"] != true || payload["skill_sync_complete"] != false || !strings.Contains(state.raptorWarning, "checksum rejected") {
		t.Fatalf("partial status=%v", payload)
	}
	if state.snapshotPath == "" {
		t.Error("Raptor failure prevented the independent MCP snapshot")
	}
}

type raptorReleaseTransport func(*http.Request) (*http.Response, error)

func (f raptorReleaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLoginInstallsMissingRaptorBinary(t *testing.T) {
	useRealRaptorSetup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	stubMCPManifestFetch(t)
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	binary := "#!/bin/sh\nexit 0\n"
	requests := 0
	http.DefaultTransport = raptorReleaseTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("Authorization") != "" {
			t.Error("control-plane credentials leaked into public release request")
		}
		var body string
		switch {
		case r.URL.Host == "api.github.com" && strings.HasSuffix(r.URL.Path, "/Facets-cloud/raptor-releases/releases/latest"):
			body = fmt.Sprintf(`{"tag_name":"v1.2.3","assets":[{"name":%q,"size":%d,"digest":"sha256:%x","browser_download_url":"https://github.com/Facets-cloud/raptor-releases/releases/download/v1.2.3/binary"}]}`, raptorAssetName(runtime.GOOS, runtime.GOARCH), len(binary), sha256.Sum256([]byte(binary)))
		case r.URL.Host == "github.com":
			body = binary
		default:
			t.Errorf("unexpected request %s", r.URL)
			return nil, fmt.Errorf("unexpected request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})
	var out bytes.Buffer
	state := runPostAuthSetup(&out, false, "https://cp.invalid", bearer("private-pat"))
	path := filepath.Join(home, ".local/bin/raptor")
	got, err := os.ReadFile(path)
	if err != nil || string(got) != binary {
		t.Fatalf("login did not install Raptor: %s %v; output=%s", got, err, out.String())
	}
	if requests != 2 {
		t.Errorf("release requests=%d, want metadata + binary", requests)
	}
	if info, _ := os.Stat(path); info.Mode()&0111 == 0 {
		t.Error("installed binary is not executable")
	}
	if setupPayload("test", "test", "https://cp.invalid", "", false, state)["raptor_binary"] == nil {
		t.Error("JSON omits Raptor installation status")
	}
}

func fakeRaptorExporter(t *testing.T, home string) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), "raptor")
	mustMkdir(t, filepath.Join(source, "references"))
	files := map[string]string{
		"SKILL.md":            "---\nname: raptor\ndescription: Canonical Raptor\nmetadata:\n  version: \"1.0\"\n---\nRead [guide](references/guide.md).\n",
		"references/guide.md": "Raptor guide from the binary.\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(source, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(home, "bin", "raptor")
	mustMkdir(t, filepath.Dir(bin))
	// The real subprocess must use a disposable home and an explicit target.
	script := "#!/bin/sh\nset -eu\n[ \"$1 $2 $3 $4\" = 'install-skills --agent claude --path' ]\n[ \"$HOME\" = \"$5\" ]\n/bin/mkdir -p \"$5/.claude/skills\"\n/bin/cp -R '" + source + "' \"$5/.claude/skills/raptor\"\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(bin))
	return bin
}

func TestBootstrapInstallsRaptorBesidePraxis(t *testing.T) {
	useRealRaptorSetup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustMkdir(t, filepath.Join(home, ".claude"))
	fakeRaptorExporter(t, home)
	if _, err := installBootstrapSkills(io.Discard, true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"praxis/SKILL.md", "raptor/SKILL.md", "raptor/references/guide.md"} {
		if _, err := os.Stat(filepath.Join(home, ".claude/skills", path)); err != nil {
			t.Errorf("skill install omitted %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".facets/last-skills-upgrade")); !os.IsNotExist(err) {
		t.Errorf("temporary export must not register a path in the real Raptor cache: %v", err)
	}
}

func TestExplicitSetupBootstrapsMissingBinaryBeforeSkillInstall(t *testing.T) {
	useRealRaptorSetup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	mustMkdir(t, filepath.Join(home, ".claude"))
	ensureRaptorBinary = func() (raptorinstall.Result, error) {
		return raptorinstall.Result{Path: fakeRaptorExporter(t, home), Installed: true}, nil
	}
	var out bytes.Buffer
	setupCmd.SetOut(&out)
	t.Cleanup(func() { setupCmd.SetOut(nil) })
	if err := setupCmd.RunE(setupCmd, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude/skills/raptor/references/guide.md")); err != nil {
		t.Fatal(err)
	}
}
