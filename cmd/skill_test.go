package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/agentcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/skillcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
)

// _credentialsPut is a small helper used by tests to seed a credentials
// file. Wraps credentials.Put so tests don't need to know the INI format.
func _credentialsPut(name, url, username, token string) error {
	return credentials.Put(name, credentials.Profile{
		URL: url, Username: username, Token: token,
	})
}

// withSeams swaps the package-level seams for one test, restoring the
// originals via t.Cleanup.
func withSeams(t *testing.T,
	detect func() []harness.Harness,
	install func(string, []harness.Harness) ([]skillinstall.Installation, error),
	list func() ([]skillinstall.Installation, error),
) {
	t.Helper()
	origD, origI, origL := detectHarnesses, installSkill, listInstalledSkill
	if detect != nil {
		detectHarnesses = detect
	}
	if install != nil {
		installSkill = install
	}
	if list != nil {
		listInstalledSkill = list
	}
	t.Cleanup(func() {
		detectHarnesses, installSkill, listInstalledSkill = origD, origI, origL
	})
}

// TestListSkills_Empty covers the AI-host path: stdout is non-TTY
// (bytes.Buffer) so UseJSON auto-resolves to true, and an empty receipt
// produces `[]` not English text — preserving the parseable-JSON
// contract every AI-callable command holds.
func TestListSkills_Empty(t *testing.T) {
	withSeams(t, nil, nil,
		func() ([]skillinstall.Installation, error) { return nil, nil })

	var buf bytes.Buffer
	listSkillsCmd.SetOut(&buf)
	if err := listSkillsCmd.RunE(listSkillsCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if strings.TrimSpace(buf.String()) != "[]" {
		t.Errorf("empty non-TTY output = %q, want []", buf.String())
	}
}

func TestListSkills_JSON(t *testing.T) {
	withSeams(t, nil, nil,
		func() ([]skillinstall.Installation, error) {
			return []skillinstall.Installation{
				{SkillName: "praxis", Harness: "claude-code", Path: "/c/praxis/SKILL.md", InstalledAt: time.Now()},
				{SkillName: "praxis", Harness: "codex", Path: "/x/praxis/SKILL.md", InstalledAt: time.Now()},
			}, nil
		})
	listSkillsJSON = true
	t.Cleanup(func() { listSkillsJSON = false })

	var buf bytes.Buffer
	listSkillsCmd.SetOut(&buf)
	if err := listSkillsCmd.RunE(listSkillsCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	var out []struct {
		SkillName string `json:"skill_name"`
		Harness   string `json:"harness"`
		Path      string `json:"path"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("--json output should be a JSON array: %v\noutput:\n%s", err, buf.String())
	}
	if len(out) != 2 || out[0].SkillName != "praxis" || out[1].Path != "/x/praxis/SKILL.md" {
		t.Errorf("unexpected entries: %+v", out)
	}
}

func TestRefreshSkills_ProjectFlag_ScopesToProjectDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubMCPManifestFetch(t)

	if err := credentialsPut("default", "https://x.test", "tester@x", "sk_test_T"); err != nil {
		t.Fatal(err)
	}

	// Project dir under the faked home (ProjectRoot discovery is bounded to
	// the home subtree).
	proj := filepath.Join(home, "repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return proj, nil }))

	withSeams(t,
		func() []harness.Harness {
			return []harness.Harness{{
				Name:     "claude-code",
				Detected: true,
				SkillDir: filepath.Join(home, ".claude", "skills"),
				AgentDir: filepath.Join(home, ".claude", "agents"),
			}}
		}, nil, nil)

	origFetchSk := fetchCatalog
	fetchCatalog = func(_, _ string) ([]skillcatalog.Skill, error) { return nil, nil }
	t.Cleanup(func() { fetchCatalog = origFetchSk })
	origFetchAg := fetchAgents
	fetchAgents = func(_, _ string) ([]agentcatalog.Agent, error) { return nil, nil }
	t.Cleanup(func() { fetchAgents = origFetchAg })

	// Drive the flags through Cobra to validate the flag wiring (not just
	// the underlying vars) so a removed/renamed binding is caught.
	if err := refreshSkillsCmd.Flags().Set("project", "true"); err != nil {
		t.Fatalf("set --project: %v", err)
	}
	if err := refreshSkillsCmd.Flags().Set("json", "true"); err != nil {
		t.Fatalf("set --json: %v", err)
	}
	t.Cleanup(func() {
		_ = refreshSkillsCmd.Flags().Set("project", "false")
		_ = refreshSkillsCmd.Flags().Set("json", "false")
	})

	var buf bytes.Buffer
	refreshSkillsCmd.SetOut(&buf)
	if err := refreshSkillsCmd.RunE(refreshSkillsCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}

	if !strings.Contains(buf.String(), `"scope": "project"`) {
		t.Errorf("JSON output should report project scope; got:\n%s", buf.String())
	}
	// Skills went to the project dir, not the user-level home dir.
	if _, err := os.Stat(filepath.Join(proj, ".claude", "skills", "praxis", "SKILL.md")); err != nil {
		t.Errorf("meta-skill should be under project dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "praxis", "SKILL.md")); err == nil {
		t.Error("--project must not write to user-level home dir")
	}
}

// TestRefreshSkills_ProjectFlag_Unresolvable_ExitsUsage pins the new
// contract: `--project` pins the cwd to the active profile, so if the cwd
// can't be made a project root (e.g. it's outside home, or getwd fails) the
// command fails fast with a Usage exit rather than silently installing
// user-level under a "project" request.
func TestRefreshSkills_ProjectFlag_Unresolvable_ExitsUsage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubMCPManifestFetch(t)

	if err := credentialsPut("default", "https://x.test", "tester@x", "sk_test_T"); err != nil {
		t.Fatal(err)
	}

	// getwd fails — the cwd can't be resolved to a project root.
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return "", errors.New("no cwd") }))
	exitCode := stubOsExit(t)

	withSeams(t,
		func() []harness.Harness {
			return []harness.Harness{{
				Name:     "claude-code",
				Detected: true,
				SkillDir: filepath.Join(home, ".claude", "skills"),
				AgentDir: filepath.Join(home, ".claude", "agents"),
			}}
		}, nil, nil)

	if err := refreshSkillsCmd.Flags().Set("project", "true"); err != nil {
		t.Fatalf("set --project: %v", err)
	}
	if err := refreshSkillsCmd.Flags().Set("json", "true"); err != nil {
		t.Fatalf("set --json: %v", err)
	}
	t.Cleanup(func() {
		_ = refreshSkillsCmd.Flags().Set("project", "false")
		_ = refreshSkillsCmd.Flags().Set("json", "false")
	})

	var buf bytes.Buffer
	refreshSkillsCmd.SetOut(&buf)
	if err := refreshSkillsCmd.RunE(refreshSkillsCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}

	if *exitCode != exitcode.Usage {
		t.Errorf("expected Usage exit (%d) when --project cwd is unresolvable, got %d", exitcode.Usage, *exitCode)
	}
}

// credentialsPut is a tiny helper to seed a profile for tests so they
// don't have to know about the INI format directly.
func credentialsPut(name, url, username, token string) error {
	return credentialsPutImpl(name, url, username, token)
}

var credentialsPutImpl = func(name, url, username, token string) error {
	return _credentialsPut(name, url, username, token)
}

// TestListSkills_Populated exercises the pretty formatter directly,
// bypassing the cobra RunE + UseJSON TTY-detection path (which would
// auto-resolve to JSON when stdout is a bytes.Buffer).
func TestListSkills_Populated(t *testing.T) {
	entries := []skillEntryForOutput{
		{SkillName: "praxis", Harness: "claude-code", Path: "/p1"},
		{SkillName: "praxis", Harness: "codex", Path: "/p2"},
	}

	var buf bytes.Buffer
	printSkillsPretty(&buf, entries)
	out := buf.String()
	for _, want := range []string{"SKILL", "HARNESS", "PATH", "praxis", "claude-code", "codex", "/p1", "/p2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, out)
		}
	}
}
