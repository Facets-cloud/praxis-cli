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
	uninstall func(string) ([]skillinstall.Installation, error),
	list func() ([]skillinstall.Installation, error),
) {
	t.Helper()
	origD, origI, origU, origL := detectHarnesses, installSkill, uninstallSkill, listInstalledSkill
	if detect != nil {
		detectHarnesses = detect
	}
	if install != nil {
		installSkill = install
	}
	if uninstall != nil {
		uninstallSkill = uninstall
	}
	if list != nil {
		listInstalledSkill = list
	}
	t.Cleanup(func() {
		detectHarnesses, installSkill, uninstallSkill, listInstalledSkill = origD, origI, origU, origL
	})
}

func TestInstallSkill_PassesPraxisName(t *testing.T) {
	// Isolate HOME so the catalog step resolves a not-logged-in profile and
	// soft-skips (no real network call against the developer's live profile).
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	var capturedName string
	withSeams(t,
		func() []harness.Harness { return []harness.Harness{{Name: "claude-code", Detected: true}} },
		func(name string, hosts []harness.Harness) ([]skillinstall.Installation, error) {
			capturedName = name
			return []skillinstall.Installation{{SkillName: name, Harness: "claude-code", Path: "/p"}}, nil
		}, nil, nil)

	installSkillCmd.SetOut(&bytes.Buffer{})
	if err := installSkillCmd.RunE(installSkillCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if capturedName != "praxis" {
		t.Errorf("install called with name %q, want praxis", capturedName)
	}
}

func TestInstallSkill_NoHosts(t *testing.T) {
	withSeams(t, func() []harness.Harness { return nil }, nil, nil, nil)

	var buf bytes.Buffer
	installSkillCmd.SetOut(&buf)
	if err := installSkillCmd.RunE(installSkillCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !strings.Contains(buf.String(), "No supported AI hosts detected") {
		t.Errorf("output = %q, want substring 'No supported AI hosts detected'", buf.String())
	}
}

func TestInstallSkill_Success(t *testing.T) {
	withSeams(t,
		func() []harness.Harness {
			return []harness.Harness{
				{Name: "claude-code", Detected: true},
				{Name: "codex", Detected: true},
			}
		},
		func(name string, hosts []harness.Harness) ([]skillinstall.Installation, error) {
			out := make([]skillinstall.Installation, 0, len(hosts))
			for _, h := range hosts {
				out = append(out, skillinstall.Installation{
					SkillName:   name,
					Harness:     h.Name,
					Path:        "/fake/" + h.Name + "/" + name + "/SKILL.md",
					InstalledAt: time.Now(),
				})
			}
			return out, nil
		},
		nil, nil)

	var buf bytes.Buffer
	installSkillCmd.SetOut(&buf)
	if err := installSkillCmd.RunE(installSkillCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := buf.String()
	for _, want := range []string{"claude-code", "codex", "/fake/claude-code/praxis/SKILL.md", "Installed \"praxis\" into 2 host(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestInstallSkill_PropagatesError(t *testing.T) {
	withSeams(t,
		func() []harness.Harness {
			return []harness.Harness{{Name: "claude-code", Detected: true}}
		},
		func(string, []harness.Harness) ([]skillinstall.Installation, error) {
			return nil, errors.New("disk full")
		},
		nil, nil)

	installSkillCmd.SetOut(&bytes.Buffer{})
	err := installSkillCmd.RunE(installSkillCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("err = %v, want substring 'disk full'", err)
	}
}

func TestUninstallSkill_NothingFound(t *testing.T) {
	withSeams(t, nil, nil,
		func(string) ([]skillinstall.Installation, error) { return nil, nil },
		nil)

	var buf bytes.Buffer
	uninstallSkillCmd.SetOut(&buf)
	if err := uninstallSkillCmd.RunE(uninstallSkillCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !strings.Contains(buf.String(), "No installations of \"praxis\"") {
		t.Errorf("output = %q, want substring 'No installations'", buf.String())
	}
}

func TestUninstallSkill_RemovesAndReports(t *testing.T) {
	withSeams(t, nil, nil,
		func(name string) ([]skillinstall.Installation, error) {
			return []skillinstall.Installation{
				{SkillName: name, Harness: "claude-code", Path: "/c"},
				{SkillName: name, Harness: "codex", Path: "/x"},
			}, nil
		},
		nil)

	var buf bytes.Buffer
	uninstallSkillCmd.SetOut(&buf)
	if err := uninstallSkillCmd.RunE(uninstallSkillCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := buf.String()
	for _, want := range []string{"claude-code", "codex", "/c", "/x", "Uninstalled \"praxis\" from 2 host(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, out)
		}
	}
}

// TestListSkills_Empty covers the AI-host path: stdout is non-TTY
// (bytes.Buffer) so UseJSON auto-resolves to true, and an empty receipt
// produces `[]` not English text — preserving the parseable-JSON
// contract every AI-callable command holds.
func TestListSkills_Empty(t *testing.T) {
	withSeams(t, nil, nil, nil,
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
	withSeams(t, nil, nil, nil,
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

func TestInstallSkill_CatalogFlow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")

	// Save credentials so ResolveActive returns a usable profile
	if err := credentialsPut("default", "https://x.test", "tester@x", "sk_test_T"); err != nil {
		t.Fatal(err)
	}

	// Seam: install meta + body-installer + catalog fetcher
	type bodyCall struct {
		name string
		body string
	}
	var bodyCalls []bodyCall

	withSeams(t,
		func() []harness.Harness {
			return []harness.Harness{{Name: "claude-code", Detected: true, SkillDir: t.TempDir()}}
		},
		func(name string, hosts []harness.Harness) ([]skillinstall.Installation, error) {
			return []skillinstall.Installation{{SkillName: name, Harness: "claude-code", Path: "/p"}}, nil
		}, nil, nil)

	origBody := installSkillBody
	installSkillBody = func(name, body string, hosts []harness.Harness) ([]skillinstall.Installation, error) {
		bodyCalls = append(bodyCalls, bodyCall{name, body})
		return []skillinstall.Installation{{SkillName: name, Harness: "claude-code", Path: "/p/" + name}}, nil
	}
	defer func() { installSkillBody = origBody }()

	origFetch := fetchCatalog
	fetchCatalog = func(baseURL, token string) ([]skillcatalog.Skill, error) {
		if baseURL != "https://x.test" || token != "sk_test_T" {
			t.Fatalf("unexpected fetcher args baseURL=%q token=%q", baseURL, token)
		}
		return []skillcatalog.Skill{
			{Name: "incident-investigator", Content: "# inv body", Scope: "global"},
			{Name: "k8s-operations", Content: "# k8s body", Scope: "global"},
		}, nil
	}
	defer func() { fetchCatalog = origFetch }()

	var buf bytes.Buffer
	installSkillCmd.SetOut(&buf)
	if err := installSkillCmd.RunE(installSkillCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}

	if len(bodyCalls) != 2 {
		t.Fatalf("expected 2 catalog installs, got %d", len(bodyCalls))
	}
	// Names must be praxis-prefixed
	if bodyCalls[0].name != "praxis-incident-investigator" {
		t.Errorf("first install name = %q", bodyCalls[0].name)
	}
	// Body has the execution preamble injected (RenderedContent) AND
	// preserves the original body content.
	if !strings.Contains(bodyCalls[0].body, "Execution context") {
		t.Errorf("first install body missing execution preamble")
	}
	if !strings.Contains(bodyCalls[0].body, "# inv body") {
		t.Errorf("first install body missing original content; got: %q", bodyCalls[0].body)
	}
	if bodyCalls[1].name != "praxis-k8s-operations" {
		t.Errorf("second install name = %q", bodyCalls[1].name)
	}

	out := buf.String()
	for _, want := range []string{
		"Installed \"praxis\" into 1 host(s)",
		"Fetching skill catalog",
		"Got 2 catalog skill(s)",
		"praxis-incident-investigator",
		"praxis-k8s-operations",
		"Installed 2 catalog skill(s) into 1 host(s)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, out)
		}
	}
}

func TestRefreshSkills_ProjectFlag_ScopesToProjectDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PRAXIS_PROFILE", "")
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
		}, nil, nil, nil)

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
	t.Setenv("PRAXIS_PROFILE", "")
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
		}, nil, nil, nil)

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

func TestInstallSkill_NotLoggedIn_SoftSkipsCatalog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	// Deliberately NOT calling credentialsPut — no profile saved.

	withSeams(t,
		func() []harness.Harness {
			return []harness.Harness{{Name: "claude-code", Detected: true, SkillDir: t.TempDir()}}
		},
		func(name string, hosts []harness.Harness) ([]skillinstall.Installation, error) {
			return []skillinstall.Installation{{SkillName: name, Harness: "claude-code", Path: "/p"}}, nil
		}, nil, nil)

	// Stub fetcher — must NOT be called when not logged in
	origFetch := fetchCatalog
	called := false
	fetchCatalog = func(baseURL, token string) ([]skillcatalog.Skill, error) {
		called = true
		return nil, nil
	}
	defer func() { fetchCatalog = origFetch }()

	var buf bytes.Buffer
	installSkillCmd.SetOut(&buf)
	if err := installSkillCmd.RunE(installSkillCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if called {
		t.Error("catalog fetcher should not be called when not logged in")
	}

	out := buf.String()
	for _, want := range []string{
		"Installed \"praxis\" into 1 host(s)",
		"Skipping org skill catalog",
		"praxis login",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, out)
		}
	}
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
