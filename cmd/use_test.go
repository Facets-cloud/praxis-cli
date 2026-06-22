package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/agentcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/skillcatalog"
)

func resetUseFlags() {
	useJSON = false
	useLocal = false
}

func TestUseCmd_SetsActiveProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetUseFlags()

	_ = credentials.Put("acme", credentials.Profile{URL: "https://acme.test", Token: "t"})

	var buf bytes.Buffer
	useCmd.SetOut(&buf)
	if err := useCmd.RunE(useCmd, []string{"acme"}); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !strings.Contains(buf.String(), `"active_profile": "acme"`) {
		t.Errorf("expected JSON active_profile: acme, got %q", buf.String())
	}

	// Verify ResolveActive now returns acme via SourceConfig.
	a, _ := credentials.ResolveActive("")
	if a.Name != "acme" || a.Source != credentials.SourceConfig {
		t.Errorf("after `use acme`, ResolveActive = %+v, want acme/config", a)
	}
}

func TestUseCmd_NonExistentProfile_Errors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetUseFlags()

	t.Skip("os.Exit(4) on missing profile; covered by manual + e2e testing")
}

// TestUseCmd_Local_PinsAndInstallsProjectScoped is the core of local mode:
// `praxis use --local acme` writes a project pointer under the cwd, the
// active profile resolves to acme via SourceProject, and the profile's
// skills install project-scoped (<repo>/.claude/skills) — not into the
// user-level home dir.
func TestUseCmd_Local_PinsAndInstallsProjectScoped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PRAXIS_PROFILE", "")
	resetUseFlags()
	stubMCPManifestFetch(t)

	if err := credentials.Put("acme", credentials.Profile{
		URL: "https://acme.test", Username: "u@acme", Token: "tok",
	}); err != nil {
		t.Fatal(err)
	}

	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return repo, nil }))

	withSeams(t, func() []harness.Harness {
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

	useLocal = true
	var buf bytes.Buffer
	useCmd.SetOut(&buf)
	if err := useCmd.RunE(useCmd, []string{"acme"}); err != nil {
		t.Fatalf("RunE err = %v", err)
	}

	// Active profile now resolves to acme via the project pointer.
	a, _ := credentials.ResolveActive("")
	if a.Name != "acme" || a.Source != credentials.SourceProject {
		t.Errorf("ResolveActive after use --local = %+v, want acme/project", a)
	}
	// Project pointer + marker written under the repo.
	if _, err := os.Stat(filepath.Join(repo, ".praxis", "config.json")); err != nil {
		t.Errorf("project config.json should exist: %v", err)
	}
	// Meta-skill installed project-scoped, NOT user-level.
	if _, err := os.Stat(filepath.Join(repo, ".claude", "skills", "praxis", "SKILL.md")); err != nil {
		t.Errorf("meta-skill should be installed under project dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "praxis", "SKILL.md")); err == nil {
		t.Error("use --local must not install to the user-level home dir")
	}
	// Receipt is project-local too.
	if _, err := os.Stat(filepath.Join(repo, ".praxis", "installed.json")); err != nil {
		t.Errorf("receipt should be project-local: %v", err)
	}
}

// TestUseCmd_Local_LoggedOutProfile_PinsButSkipsInstall verifies a profile
// with no stored token still gets pinned, but skill install is skipped.
func TestUseCmd_Local_LoggedOutProfile_PinsButSkipsInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PRAXIS_PROFILE", "")
	resetUseFlags()

	// Profile exists but has no token (logged out).
	if err := credentials.Put("acme", credentials.Profile{URL: "https://acme.test"}); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return repo, nil }))

	useLocal = true
	var buf bytes.Buffer
	useCmd.SetOut(&buf)
	if err := useCmd.RunE(useCmd, []string{"acme"}); err != nil {
		t.Fatalf("RunE err = %v", err)
	}

	// Pinned: pointer written and resolves.
	a, _ := credentials.ResolveActive("")
	if a.Name != "acme" || a.Source != credentials.SourceProject {
		t.Errorf("ResolveActive = %+v, want acme/project", a)
	}
	// No skills installed (no receipt written by an install).
	if _, err := os.Stat(filepath.Join(repo, ".claude", "skills", "praxis", "SKILL.md")); err == nil {
		t.Error("logged-out profile must not trigger a skill install")
	}
	// Output is auto-JSON (non-TTY): the skip is reported as skills_installed=false.
	if !strings.Contains(buf.String(), `"skills_installed": false`) {
		t.Errorf("expected skills_installed=false in JSON, got:\n%s", buf.String())
	}
}
