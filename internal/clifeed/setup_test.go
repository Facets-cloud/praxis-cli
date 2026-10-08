package clifeed

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

func TestInstallMethod(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the cases are Unix paths; TestTildeUsesSlashes runs everywhere")
	}
	home := "/Users/dev"
	for path, want := range map[string]string{
		"/opt/homebrew/Caskroom/praxis/2.0.0/praxis_darwin_arm64": "brew",
		"/usr/local/Caskroom/praxis/2.0.0/praxis_darwin_amd64":    "brew",
		"/Users/dev/.local/bin/praxis":                            "local-bin",
		"/usr/local/bin/praxis":                                   "usr-local-bin",
		"/Users/dev/go/bin/praxis":                                "go",
		"/tmp/praxis":                                             "other",
	} {
		if got := installMethod(path, home); got != want {
			t.Errorf("installMethod(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestTilde(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the cases are Unix paths; TestTildeUsesSlashes runs everywhere")
	}
	if got := tilde("/Users/dev/.local/bin/praxis", "/Users/dev"); got != "~/.local/bin/praxis" {
		t.Errorf("tilde = %q", got)
	}
	if got := tilde("/Users/devx/praxis", "/Users/dev"); got != "/Users/devx/praxis" {
		t.Errorf("tilde of a sibling folder = %q", got)
	}
}

// fakePraxis writes an executable named praxis that prints out, and records
// its arguments and environment beside itself.
func fakePraxis(t *testing.T, dir, out string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "praxis")
	script := "#!/bin/sh\necho \"$@\" > \"$0.args\"\nenv > \"$0.env\"\ncat <<'OUT'\n" + out + "\nOUT\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPathCopiesAsksOtherCopiesSafely(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fakes need a Unix shell")
	}
	// A loaded test machine can take more than the production second to start a shell.
	orig := copyVersionTimeout
	copyVersionTimeout = 30 * time.Second
	t.Cleanup(func() { copyVersionTimeout = orig })
	home := t.TempDir()
	a := fakePraxis(t, filepath.Join(home, "a"), `{"go": "go1.24.13", "version": "2.0.0"}`)
	b := fakePraxis(t, filepath.Join(home, "b"), "praxis version v1.9.0\n  go: go1.24.0")
	self := fakePraxis(t, filepath.Join(home, "self"), "never run")
	resolvedSelf, _ := filepath.EvalSymlinks(self)

	pathEnv := strings.Join([]string{filepath.Dir(a), filepath.Dir(self), filepath.Dir(b)}, string(os.PathListSeparator))
	got := pathCopies(pathEnv, resolvedSelf, home)

	want := []pathCopy{{"~/a/praxis", "2.0.0"}, {"~/self/praxis", "dev"}, {"~/b/praxis", "1.9.0"}}
	if len(got) != 3 || got[0] != want[0] || got[2] != want[2] || got[1].Path != want[1].Path {
		t.Fatalf("pathCopies = %+v", got)
	}
	args, _ := os.ReadFile(a + ".args")
	if strings.TrimSpace(string(args)) != "version" {
		t.Errorf("args = %q, want `version` (not --version)", args)
	}
	env, _ := os.ReadFile(a + ".env")
	if !strings.Contains(string(env), "PRAXIS_NO_UPDATE_CHECK=1") {
		t.Error("the copy ran with the update check on")
	}
	if _, err := os.Stat(self + ".args"); err == nil {
		t.Error("the running binary ran itself")
	}
}

func TestSkillStateCountsFromTheReceipt(t *testing.T) {
	home := isolate(t)
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return home, nil }))
	receipt := `{"skills": [
	  {"skill_name": "praxis", "harness": "claude-code", "path": ` + jsonString(filepath.Join(home, ".claude", "skills", "praxis", "SKILL.md")) + `, "source": "embedded", "digest": "515c9936fae88c6328dfbdf6"},
	  {"skill_name": "praxis-cloud-operations", "harness": "claude-code", "path": "x", "source": "catalog"},
	  {"skill_name": "praxis-cloud-operations", "harness": "codex", "path": "x", "source": "catalog", "scope": "organization"},
	  {"skill_name": "praxis-team-runbook", "harness": "claude-code", "path": "x", "source": "catalog"}]}`
	if err := os.MkdirAll(filepath.Join(home, ".praxis"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".praxis", "installed.json"), []byte(receipt), 0o600); err != nil {
		t.Fatal(err)
	}

	skills, legacy, catalog := skillState(home)
	if len(skills) != 1 || skills[0] != (skillCopy{"praxis", "claude-code", "~/.claude/skills/praxis", "515c9936fae8"}) {
		t.Errorf("skills = %+v", skills)
	}
	// The organization namesake is not a legacy copy.
	if legacy != 1 || catalog != 2 {
		t.Errorf("legacy = %d, catalog = %d; want 1, 2", legacy, catalog)
	}
}

func TestCheckCarriesTheSetupButNoProfileList(t *testing.T) {
	isolate(t)
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(feedAnswer))
	}))
	defer srv.Close()
	t.Setenv("FACETS_CLI_FEED_URL", srv.URL)

	if _, err := Target("praxis"); err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	var s map[string]json.RawMessage
	if err := json.Unmarshal(body["setup"], &s); err != nil || s["install_method"] == nil {
		t.Fatalf("setup = %s, %v", body["setup"], err)
	}
	if _, ok := s["profiles"]; ok {
		t.Error("praxis must not send the profile list")
	}
	if strings.Contains(string(raw), "t0k") {
		t.Error("the token left the machine")
	}
}

// A failed raptor skill install reaches the snapshot until an install succeeds,
// and it changes the skills key so the census hears of it.
func TestRaptorSkillErrorReachesTheSnapshot(t *testing.T) {
	home := isolate(t)
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return home, nil }))
	before := SkillsKey()

	RecordRaptorSkillError(errors.New("raptor install skill: exit status 137"))
	if got := collectSetup().SkillsError; got != "raptor install skill: exit status 137" {
		t.Errorf("skills_error = %q", got)
	}
	if SkillsKey() == before {
		t.Error("the skills key did not change after a failed install")
	}

	RecordRaptorSkillError(nil)
	if got := collectSetup().SkillsError; got != "" {
		t.Errorf("skills_error after a good install = %q, want empty", got)
	}
	if SkillsKey() != before {
		t.Error("the skills key did not come back after a good install")
	}
}

// The skills key follows the receipt, so a skill change sends a new check.
func TestSkillsKeyFollowsTheReceipt(t *testing.T) {
	home := isolate(t)
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return home, nil }))
	empty := SkillsKey()
	if err := os.MkdirAll(filepath.Join(home, ".praxis"), 0o700); err != nil {
		t.Fatal(err)
	}
	receipt := `{"skills": [{"skill_name": "praxis-cloud-operations", "harness": "claude-code", "path": "x", "source": "catalog"}]}`
	if err := os.WriteFile(filepath.Join(home, ".praxis", "installed.json"), []byte(receipt), 0o600); err != nil {
		t.Fatal(err)
	}
	if SkillsKey() == empty {
		t.Error("the skills key did not change with the receipt")
	}
}

// The census holds "~/" paths with forward slashes on every platform, also for
// a home with a trailing separator.
func TestTildeUsesSlashes(t *testing.T) {
	home := t.TempDir()
	for _, h := range []string{home, home + string(filepath.Separator)} {
		if got := tilde(filepath.Join(home, ".local", "bin", "praxis"), h); got != "~/.local/bin/praxis" {
			t.Errorf("tilde(home=%q) = %q, want ~/.local/bin/praxis", h, got)
		}
		if got := tilde(home, h); got != "~" {
			t.Errorf("tilde(home itself, %q) = %q, want ~", h, got)
		}
	}
}

// jsonString quotes s for a hand-written JSON fixture; a Windows path holds
// backslashes.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
