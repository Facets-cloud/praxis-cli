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

	"github.com/Facets-cloud/praxis-cli/internal/agentcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/raptorinstall"
	"github.com/Facets-cloud/praxis-cli/internal/selfupdate"
	"github.com/Facets-cloud/praxis-cli/internal/skillcatalog"
)

// useRealRaptorSetup undoes TestMain's no-op stubs for one test.
func useRealRaptorSetup(t *testing.T) {
	t.Helper()
	oldEnsure, oldUpdate := ensureRaptorBinary, updateRaptor
	ensureRaptorBinary, updateRaptor = raptorinstall.Ensure, runRaptorUpgrade
	t.Cleanup(func() { ensureRaptorBinary, updateRaptor = oldEnsure, oldUpdate })
}

// fakeRaptor puts a raptor on PATH that reports version and records the
// arguments of its upgrade call in the returned marker file.
func fakeRaptor(t *testing.T, home, version string, upgradeExit int) (marker string) {
	t.Helper()
	bin := filepath.Join(home, "bin", "raptor")
	mustMkdir(t, filepath.Dir(bin))
	marker = filepath.Join(home, "upgrade-args")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'raptor version %s'; exit 0; fi\n"+
		"printf '%%s' \"$*\" > '%s'\necho 'upgrade output'\nexit %d\n", version, marker, upgradeExit)
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(bin))
	return marker
}

func TestUpdateAlsoUpgradesRaptor(t *testing.T) {
	tests := []struct {
		name        string
		tag         string
		brew        bool
		raptorExit  int
		wantUpdated bool
		wantReason  string
		wantPraxis  string
	}{
		{"praxis current", "v" + version, false, 0, false, "already_latest", "old-praxis"},
		{"praxis updated", "v999.0.0", false, 0, true, "", "new-praxis"},
		{"homebrew praxis", "v999.0.0", true, 0, false, "homebrew_managed", "old-praxis"},
		{"raptor upgrade fails", "v999.0.0", false, 6, true, "", "new-praxis"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useRealRaptorSetup(t)
			home := t.TempDir()
			t.Setenv("HOME", home)
			marker := fakeRaptor(t, home, "0.1.100", tc.raptorExit)

			self := filepath.Join(home, "praxis")
			if tc.brew {
				self = filepath.Join(home, "Caskroom", "praxis", "1", "praxis")
			}
			mustMkdir(t, filepath.Dir(self))
			if err := os.WriteFile(self, []byte("old-praxis"), 0700); err != nil {
				t.Fatal(err)
			}
			withSelfPath(t, self)
			withFakeRelease(t, &selfupdate.Release{TagName: tc.tag, Assets: []selfupdate.Asset{{Name: "praxis_" + runtime.GOOS + "_" + runtime.GOARCH}}}, nil)
			origDownload := downloadAsset
			downloadAsset = func(string, string) (string, error) {
				p := filepath.Join(home, "new-praxis")
				return p, os.WriteFile(p, []byte("new-praxis"), 0700)
			}
			t.Cleanup(func() { downloadAsset = origDownload; updateCmd.SetOut(nil) })

			var out bytes.Buffer
			updateCmd.SetOut(&out)
			err := updateCmd.RunE(updateCmd, nil)
			if (err != nil) != (tc.raptorExit != 0) {
				t.Fatalf("err = %v, output = %s", err, out.String())
			}
			var payload struct {
				Updated bool                `json:"updated"`
				Reason  string              `json:"reason"`
				Raptor  raptorUpgradeResult `json:"raptor"`
			}
			if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
				t.Fatalf("raptor output corrupted the JSON: %s", out.String())
			}
			if payload.Updated != tc.wantUpdated || payload.Reason != tc.wantReason {
				t.Errorf("praxis outcome = %+v", payload)
			}
			if !payload.Raptor.Attempted || payload.Raptor.Completed != (tc.raptorExit == 0) {
				t.Errorf("raptor outcome = %+v", payload.Raptor)
			}
			if args, _ := os.ReadFile(marker); string(args) != "upgrade --yes" {
				t.Errorf("raptor ran with %q, want \"upgrade --yes\"", args)
			}
			if got, _ := os.ReadFile(self); string(got) != tc.wantPraxis {
				t.Errorf("praxis binary = %q, want %q", got, tc.wantPraxis)
			}
		})
	}
}

func TestRunRaptorUpgrade(t *testing.T) {
	t.Run("interactive streams raptor and passes no --yes", func(t *testing.T) {
		useRealRaptorSetup(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		marker := fakeRaptor(t, home, "0.1.100", 0)
		var out bytes.Buffer
		res, err := runRaptorUpgrade(&out, false, false)
		if err != nil || !res.Completed || !strings.Contains(out.String(), "upgrade output") {
			t.Fatalf("res = %+v, err = %v, out = %s", res, err, out.String())
		}
		if args, _ := os.ReadFile(marker); string(args) != "upgrade" {
			t.Errorf("raptor ran with %q", args)
		}
	})
	t.Run("development build is left alone", func(t *testing.T) {
		useRealRaptorSetup(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		marker := fakeRaptor(t, home, "0.development", 0)
		res, err := runRaptorUpgrade(io.Discard, true, true)
		if err != nil || res.Attempted || !strings.Contains(res.Warning, "development build") {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Error("raptor upgrade ran on a development build")
		}
	})
	t.Run("install failure is reported, nothing runs", func(t *testing.T) {
		useRealRaptorSetup(t)
		ensureRaptorBinary = func() (raptorinstall.Result, error) {
			return raptorinstall.Result{}, errors.New("download unavailable")
		}
		res, err := runRaptorUpgrade(io.Discard, true, true)
		if err == nil || res.Attempted || !strings.Contains(res.Warning, "download unavailable") {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
	})
}

type raptorReleaseTransport func(*http.Request) (*http.Response, error)

func (f raptorReleaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Login installs a missing raptor from the public release, verified by digest,
// and never sends the control-plane token to GitHub.
func TestLoginInstallsMissingRaptor(t *testing.T) {
	useRealRaptorSetup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	stubMCPManifestFetch(t)
	binary := "#!/bin/sh\nexit 0\n"
	requests := 0
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })
	http.DefaultTransport = raptorReleaseTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("Authorization") != "" {
			t.Error("control-plane credentials sent to a public release host")
		}
		var body string
		switch {
		case r.URL.Host == "api.github.com" && strings.HasSuffix(r.URL.Path, "/Facets-cloud/raptor-releases/releases/latest"):
			body = fmt.Sprintf(`{"assets":[{"name":%q,"size":%d,"digest":"sha256:%x","browser_download_url":"https://github.com/Facets-cloud/raptor-releases/releases/download/v1.2.3/bin"}]}`,
				raptorAssetName(runtime.GOOS, runtime.GOARCH), len(binary), sha256.Sum256([]byte(binary)))
		case r.URL.Host == "github.com":
			body = binary
		default:
			t.Errorf("unexpected request %s", r.URL)
			return nil, errors.New("unexpected request")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})

	state := runPostAuthSetup(io.Discard, true, "https://cp.invalid", bearer("private-pat"))
	path := filepath.Join(home, ".local", "bin", "raptor")
	if got, err := os.ReadFile(path); err != nil || string(got) != binary {
		t.Fatalf("raptor not installed: %q, %v", got, err)
	}
	if info, _ := os.Stat(path); info.Mode()&0111 == 0 {
		t.Error("installed raptor is not executable")
	}
	if requests != 2 {
		t.Errorf("requests = %d, want release metadata + binary", requests)
	}
	got, ok := setupPayload("p", "u", "https://cp.invalid", "", false, state)["raptor_binary"].(raptorinstall.Result)
	if !ok || !got.Installed || got.Path != path {
		t.Errorf("raptor_binary = %+v", got)
	}
}

func TestLoginContinuesWhenRaptorInstallFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stubMCPManifestFetch(t)
	orig := ensureRaptorBinary
	ensureRaptorBinary = func() (raptorinstall.Result, error) {
		return raptorinstall.Result{}, errors.New("checksum rejected")
	}
	t.Cleanup(func() { ensureRaptorBinary = orig })

	state := runPostAuthSetup(io.Discard, true, "https://cp.invalid", bearer("tok"))
	if !strings.Contains(state.raptorWarning, "checksum rejected") {
		t.Errorf("raptorWarning = %q", state.raptorWarning)
	}
	if state.snapshotPath == "" {
		t.Error("a raptor failure stopped the rest of login setup")
	}
}

func TestSetupInstallsRaptorButFirstRunDoesNot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	calls := 0
	orig := ensureRaptorBinary
	ensureRaptorBinary = func() (raptorinstall.Result, error) {
		calls++
		return raptorinstall.Result{}, errors.New("offline")
	}
	t.Cleanup(func() { ensureRaptorBinary = orig; setupCmd.SetOut(nil) })

	maybeFirstRunBootstrap([]string{"status"})
	if calls != 0 {
		t.Fatalf("first run tried to install raptor %d time(s); it must stay offline", calls)
	}

	var out bytes.Buffer
	setupCmd.SetOut(&out)
	if err := setupCmd.RunE(setupCmd, nil); err != nil {
		t.Fatalf("a raptor failure must not fail setup: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("setup JSON: %v\n%s", err, out.String())
	}
	if calls != 1 || payload["raptor_warning"] != "offline" {
		t.Errorf("calls = %d, payload = %v", calls, payload)
	}
}

// skillRaptor is a fake raptor that acts like `raptor install skill`: it
// writes <base>/.<agent>/skills/raptor/SKILL.md, where base is --path or
// HOME, and appends its arguments to the returned log. failAgent fails.
func skillRaptor(t *testing.T, version, failAgent string) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "raptor"), filepath.Join(dir, "calls")
	script := `#!/bin/sh
if [ "$1" = --version ]; then [ '` + version + `' = FAIL ] && exit 1; echo "raptor version ` + version + `"; exit 0; fi
echo "$*" >> '` + log + `'
agent=$4; base=$HOME
[ "$5" = --path ] && base=$6
[ "$agent" = '` + failAgent + `' ] && { echo "boom for $agent" >&2; exit 3; }
mkdir -p "$base/.$agent/skills/raptor" && echo raptor > "$base/.$agent/skills/raptor/SKILL.md"
`
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestInstallRaptorSkills(t *testing.T) {
	user := func(home string) []harness.Harness {
		return []harness.Harness{
			{Name: "claude-code", SkillDir: filepath.Join(home, ".claude", "skills")},
			{Name: "codex", SkillDir: filepath.Join(home, ".agents", "skills")},
			{Name: "gemini-cli", SkillDir: filepath.Join(home, ".agents", "skills")},
			{Name: "antigravity", SkillDir: filepath.Join(home, ".gemini", "config", "skills")},
		}
	}
	tests := []struct {
		name, version, fail string
		sharedRaptor        bool
		wantCalls           []string
		wantPaths           []string
		wantErr             string
	}{
		{name: "every raptor agent, antigravity skipped", version: "0.1.121",
			wantCalls: []string{"install skill --agent claude", "install skill --agent codex", "install skill --agent gemini"},
			wantPaths: []string{".claude/skills/raptor/SKILL.md", ".codex/skills/raptor/SKILL.md", ".gemini/skills/raptor/SKILL.md"}},
		{name: "shared root already has raptor", version: "0.1.121", sharedRaptor: true,
			wantCalls: []string{"install skill --agent claude"},
			wantPaths: []string{".claude/skills/raptor/SKILL.md"}},
		{name: "development build may install", version: "0.development",
			wantCalls: []string{"install skill --agent claude", "install skill --agent codex", "install skill --agent gemini"},
			wantPaths: []string{".claude/skills/raptor/SKILL.md", ".codex/skills/raptor/SKILL.md", ".gemini/skills/raptor/SKILL.md"}},
		{name: "raptor too old", version: "0.1.100", wantErr: "raptor 0.1.100 is too old"},
		{name: "raptor does not answer --version", version: "FAIL", wantErr: "did not answer --version"},
		{name: "one agent fails, the others install", version: "0.1.121", fail: "codex",
			wantCalls: []string{"install skill --agent claude", "install skill --agent codex", "install skill --agent gemini"},
			wantPaths: []string{".claude/skills/raptor/SKILL.md", ".gemini/skills/raptor/SKILL.md"},
			wantErr:   "raptor skill for codex"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			bin, log := skillRaptor(t, tc.version, tc.fail)
			if tc.sharedRaptor {
				mustMkdir(t, filepath.Join(home, ".agents", "skills", "raptor"))
				if err := os.WriteFile(filepath.Join(home, ".agents", "skills", "raptor", "SKILL.md"), []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
			}

			got, err := runInstallRaptorSkills(bin, user(home))
			if tc.wantErr == "" && err != nil {
				t.Fatalf("err = %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if c := calls(t, log); strings.Join(c, "|") != strings.Join(tc.wantCalls, "|") {
				t.Errorf("raptor calls = %q, want %q", c, tc.wantCalls)
			}
			var paths []string
			for _, in := range got {
				rel, _ := filepath.Rel(home, in.Path)
				paths = append(paths, rel)
				if _, err := os.Stat(in.Path); err != nil {
					t.Errorf("reported %s but it is not on disk", in.Path)
				}
			}
			if strings.Join(paths, "|") != strings.Join(tc.wantPaths, "|") {
				t.Errorf("paths = %q, want %q", paths, tc.wantPaths)
			}
		})
	}
	if _, err := runInstallRaptorSkills("", nil); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("no raptor: err = %v", err)
	}
}

// Login asks raptor to install its skill for the detected hosts and reports
// it; a failure is a warning and the rest of login still runs. A
// project-scoped login still installs the raptor skill at user level.
func TestLoginInstallsRaptorSkill(t *testing.T) {
	for _, tc := range []struct {
		fail    string
		project bool
	}{{"", false}, {"claude", false}, {"", true}} {
		fail := tc.fail
		t.Run(fmt.Sprintf("fail=%s,project=%t", tc.fail, tc.project), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			setRootProfile(t, "")
			stubMCPManifestFetch(t)
			if tc.project {
				pinProjectRoot(t, home)
			}
			bin, log := skillRaptor(t, "0.1.121", fail)
			origEnsure, origDetect, origFetch, origAgents := ensureRaptorBinary, detectHarnesses, fetchCatalog, fetchAgents
			ensureRaptorBinary = func() (raptorinstall.Result, error) { return raptorinstall.Result{Path: bin}, nil }
			// Codex already reads a raptor skill from the user-level shared root,
			// so only Claude may get one, also in project scope.
			detectHarnesses = func() []harness.Harness {
				return []harness.Harness{
					{Name: "claude-code", SkillDir: filepath.Join(home, ".claude", "skills")},
					{Name: "codex", SkillDir: filepath.Join(home, ".agents", "skills")},
				}
			}
			mustMkdir(t, filepath.Join(home, ".agents", "skills", "raptor"))
			if err := os.WriteFile(filepath.Join(home, ".agents", "skills", "raptor", "SKILL.md"), []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
			fetchCatalog = func(string, map[string]string, ...string) ([]skillcatalog.Skill, error) { return nil, nil }
			fetchAgents = func(string, map[string]string) ([]agentcatalog.Agent, error) { return nil, nil }
			t.Cleanup(func() {
				ensureRaptorBinary, detectHarnesses, fetchCatalog, fetchAgents = origEnsure, origDetect, origFetch, origAgents
			})

			state := runPostAuthSetup(io.Discard, true, "https://cp.invalid", bearer("tok"))
			if state.projectScoped != tc.project {
				t.Fatalf("projectScoped = %t, want %t", state.projectScoped, tc.project)
			}
			if c := calls(t, log); len(c) != 1 || c[0] != "install skill --agent claude" {
				t.Errorf("raptor calls = %q", c)
			}
			payload := setupPayload("p", "u", "https://cp.invalid", "", false, state)
			skills, _ := payload["raptor_skills"].([]skillInstallationLite)
			if fail == "" && (len(skills) != 1 || skills[0].Path != filepath.Join(home, ".claude", "skills", "raptor", "SKILL.md") || state.raptorWarning != "") {
				t.Errorf("raptor_skills = %+v, warning = %q", skills, state.raptorWarning)
			}
			if fail != "" && (len(skills) != 0 || !strings.Contains(state.raptorWarning, "boom for claude")) {
				t.Errorf("raptor_skills = %+v, warning = %q", skills, state.raptorWarning)
			}
			if state.snapshotPath == "" {
				t.Error("a raptor skill failure stopped the rest of login")
			}
		})
	}
}

// Explicit setup installs the raptor skill; the silent first run never runs raptor.
func TestSetupInstallsRaptorSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setRootProfile(t, "")
	mustMkdir(t, filepath.Join(home, ".claude"))
	bin, log := skillRaptor(t, "0.1.121", "")
	orig, origDetect := ensureRaptorBinary, detectHarnesses
	ensureRaptorBinary = func() (raptorinstall.Result, error) { return raptorinstall.Result{Path: bin}, nil }
	detectHarnesses = func() []harness.Harness {
		return []harness.Harness{{Name: "claude-code", SkillDir: filepath.Join(home, ".claude", "skills")}}
	}
	t.Cleanup(func() { ensureRaptorBinary, detectHarnesses = orig, origDetect; setupCmd.SetOut(nil) })

	maybeFirstRunBootstrap([]string{"status"})
	if c := calls(t, log); len(c) != 0 {
		t.Fatalf("first run ran raptor: %q", c)
	}

	var out bytes.Buffer
	setupCmd.SetOut(&out)
	if err := setupCmd.RunE(setupCmd, nil); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		RaptorSkills []skillInstallationLite `json:"raptor_skills"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("setup JSON: %v\n%s", err, out.String())
	}
	if len(payload.RaptorSkills) != 1 || payload.RaptorSkills[0].Harness != "claude-code" {
		t.Errorf("raptor_skills = %+v", payload.RaptorSkills)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "raptor", "SKILL.md")); err != nil {
		t.Error(err)
	}
}

// stubRaptorLifecycle replaces the raptor seams. ensure returns paths in
// order, then repeats the last; it records upgrade calls and the raptor path
// each skill install used.
func stubRaptorLifecycle(t *testing.T, ensure []raptorinstall.Result, upgradeOK bool, skillErr error) (upgrades *int, skillPaths *[]string) {
	t.Helper()
	upgrades, skillPaths = new(int), &[]string{}
	origEnsure, origUpdate, origSkills, origDetect := ensureRaptorBinary, updateRaptor, installRaptorSkills, detectHarnesses
	i := 0
	ensureRaptorBinary = func() (raptorinstall.Result, error) {
		r := ensure[min(i, len(ensure)-1)]
		i++
		if r.Path == "" {
			return r, errors.New("raptor unavailable")
		}
		return r, nil
	}
	updateRaptor = func(io.Writer, bool, bool) (raptorUpgradeResult, error) {
		*upgrades++
		if !upgradeOK {
			return raptorUpgradeResult{Attempted: true}, errors.New("upgrade failed")
		}
		return raptorUpgradeResult{Attempted: true, Completed: true}, nil
	}
	installRaptorSkills = func(path string, _ []harness.Harness) ([]skillInstallationLite, error) {
		*skillPaths = append(*skillPaths, path)
		if skillErr != nil {
			return nil, skillErr
		}
		return []skillInstallationLite{{Harness: "claude-code", Path: "/h/.claude/skills/raptor/SKILL.md"}}, nil
	}
	detectHarnesses = func() []harness.Harness { return nil }
	t.Cleanup(func() {
		ensureRaptorBinary, updateRaptor, installRaptorSkills, detectHarnesses = origEnsure, origUpdate, origSkills, origDetect
	})
	return upgrades, skillPaths
}

// setup runs on every `brew upgrade`: it upgrades an existing raptor, then has
// the raptor now on PATH refresh its skill.
func TestSetupUpgradesRaptorThenRefreshesItsSkill(t *testing.T) {
	old := raptorinstall.Result{Path: "/usr/local/bin/raptor"}
	moved := raptorinstall.Result{Path: "/home/.local/bin/raptor"}
	fresh := raptorinstall.Result{Path: "/home/.local/bin/raptor", Installed: true}
	tests := []struct {
		name         string
		ensure       []raptorinstall.Result
		upgradeOK    bool
		wantUpgrades int
		wantSkillAt  []string
		wantWarning  string
	}{
		{"existing raptor is upgraded; skill uses the raptor now on PATH", []raptorinstall.Result{old, moved}, true, 1, []string{moved.Path}, ""},
		{"raptor installed just now is not upgraded", []raptorinstall.Result{fresh}, true, 0, []string{fresh.Path}, ""},
		{"failed upgrade is a warning; the skill is still refreshed", []raptorinstall.Result{old}, false, 1, []string{old.Path}, "upgrade failed"},
		{"no raptor: no upgrade, no skill", []raptorinstall.Result{{}}, true, 0, nil, "raptor unavailable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			setRootProfile(t, "")
			upgrades, skillPaths := stubRaptorLifecycle(t, tc.ensure, tc.upgradeOK, nil)
			var out bytes.Buffer
			setupCmd.SetOut(&out)
			t.Cleanup(func() { setupCmd.SetOut(nil) })
			if err := setupCmd.RunE(setupCmd, nil); err != nil {
				t.Fatalf("a raptor problem must not fail setup: %v", err)
			}
			if *upgrades != tc.wantUpgrades {
				t.Errorf("upgrades = %d, want %d", *upgrades, tc.wantUpgrades)
			}
			if strings.Join(*skillPaths, "|") != strings.Join(tc.wantSkillAt, "|") {
				t.Errorf("skill installed with %q, want %q", *skillPaths, tc.wantSkillAt)
			}
			var payload map[string]any
			if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
				t.Fatalf("setup JSON: %v\n%s", err, out.String())
			}
			if _, ok := payload["raptor_upgrade"]; ok != (tc.wantUpgrades > 0) {
				t.Errorf("raptor_upgrade present = %t", ok)
			}
			if w, _ := payload["raptor_warning"].(string); !strings.Contains(w, tc.wantWarning) || (tc.wantWarning == "" && w != "") {
				t.Errorf("raptor_warning = %q, want %q", w, tc.wantWarning)
			}
		})
	}
}

// praxis update refreshes the raptor skill right after raptor upgrades; a
// skill failure is a warning and does not fail the update.
func TestUpdateRefreshesRaptorSkillAfterUpgrade(t *testing.T) {
	tests := []struct {
		name        string
		upgradeOK   bool
		skillErr    error
		wantSkills  int
		wantWarning string
		wantErr     bool
	}{
		{"upgrade then skill refresh", true, nil, 1, "", false},
		{"skill refresh fails", true, errors.New("no skill"), 1, "no skill", false},
		{"upgrade fails: no skill refresh", false, nil, 0, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			_, skillPaths := stubRaptorLifecycle(t, []raptorinstall.Result{{Path: "/bin/raptor"}}, tc.upgradeOK, tc.skillErr)
			withFakeRelease(t, &selfupdate.Release{TagName: "v" + version}, nil)
			var out bytes.Buffer
			updateCmd.SetOut(&out)
			t.Cleanup(func() { updateCmd.SetOut(nil) })
			err := updateCmd.RunE(updateCmd, nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v", err)
			}
			if len(*skillPaths) != tc.wantSkills {
				t.Errorf("skill refreshes = %d, want %d", len(*skillPaths), tc.wantSkills)
			}
			var payload map[string]any
			if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
				t.Fatalf("update JSON: %v\n%s", err, out.String())
			}
			if w, _ := payload["raptor_skill_warning"].(string); w != tc.wantWarning {
				t.Errorf("raptor_skill_warning = %q, want %q", w, tc.wantWarning)
			}
		})
	}
}

func TestRaptorSkillEverywhere(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claude := harness.Harness{Name: "claude-code", SkillDir: filepath.Join(home, ".claude", "skills")}
	codex := harness.Harness{Name: "codex", SkillDir: filepath.Join(home, ".agents", "skills")}
	anti := harness.Harness{Name: "antigravity", SkillDir: filepath.Join(home, ".gemini", "config", "skills")}
	put := func(dir string) {
		mustMkdir(t, filepath.Join(dir, "raptor"))
		if err := os.WriteFile(filepath.Join(dir, "raptor", "SKILL.md"), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if raptorSkillEverywhere([]harness.Harness{claude, codex}) {
		t.Error("no raptor skill anywhere reported as everywhere")
	}
	put(claude.SkillDir)
	if raptorSkillEverywhere([]harness.Harness{claude, codex}) {
		t.Error("codex has no raptor skill, yet everywhere")
	}
	put(filepath.Join(home, ".codex", "skills")) // raptor's own folder for codex
	if !raptorSkillEverywhere([]harness.Harness{claude, codex, anti}) {
		t.Error("claude and codex have it; antigravity must not count")
	}
	if raptorSkillEverywhere([]harness.Harness{anti}) {
		t.Error("no raptor-supported host at all must be false")
	}
}

// Login claims praxis-v1 when the praxis skill installed and raptor-v1 only
// when raptor's skill is on every detected raptor host.
func TestLoginClaimsCapabilities(t *testing.T) {
	for _, tc := range []struct {
		raptor bool
		want   string
	}{{false, "[praxis-v1]"}, {true, "[praxis-v1 raptor-v1]"}} {
		t.Run(fmt.Sprint(tc.raptor), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			setRootProfile(t, "")
			stubMCPManifestFetch(t)
			claudeDir := filepath.Join(home, ".claude", "skills")
			if tc.raptor {
				mustMkdir(t, filepath.Join(claudeDir, "raptor"))
				if err := os.WriteFile(filepath.Join(claudeDir, "raptor", "SKILL.md"), []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var got []string
			origDetect, origFetch, origAgents := detectHarnesses, fetchCatalog, fetchAgents
			detectHarnesses = func() []harness.Harness {
				return []harness.Harness{{Name: "claude-code", SkillDir: claudeDir}}
			}
			fetchCatalog = func(_ string, _ map[string]string, caps ...string) ([]skillcatalog.Skill, error) {
				got = caps
				return nil, nil
			}
			fetchAgents = func(string, map[string]string) ([]agentcatalog.Agent, error) { return nil, nil }
			t.Cleanup(func() { detectHarnesses, fetchCatalog, fetchAgents = origDetect, origFetch, origAgents })

			runPostAuthSetup(io.Discard, true, "https://cp.invalid", bearer("tok"))
			if fmt.Sprint(got) != tc.want {
				t.Errorf("caps = %v, want %s", got, tc.want)
			}
		})
	}
}
