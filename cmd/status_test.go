package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

func resetStatusFlags() {
	statusJSON = false
	statusFull = false
}

func TestStatusCmd_LocalMode_ReportsProjectRootAndSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resetStatusFlags()

	if err := credentials.Put("acme", credentials.Profile{URL: "https://acme.test", Username: "u@acme", Token: "tok"}); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(paths.SetGetwdForTest(func() (string, error) { return repo, nil }))
	if _, err := credentials.SetActiveLocal("acme"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"profile": "acme"`, `"profile_source": "project"`, `"project_root"`} {
		if !strings.Contains(out, want) {
			t.Errorf("status in local mode missing %q\nfull: %s", want, out)
		}
	}
}

func TestStatusCmd_IncludesToolsFreshness(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetStatusFlags()
	withVersion(t, "dev") // praxis not checkable → no network in freshCached
	origV := raptorLocalVersion
	t.Cleanup(func() { raptorLocalVersion = origV })
	raptorLocalVersion = func() (string, bool) { return "0.1.0", true }

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	var s map[string]any
	if err := json.Unmarshal(buf.Bytes(), &s); err != nil {
		t.Fatalf("status not JSON: %v\n%s", err, buf.String())
	}
	tools, ok := s["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("tools block missing or wrong size: %v", s["tools"])
	}
	names := map[string]bool{}
	for _, tv := range tools {
		names[tv.(map[string]any)["tool"].(string)] = true
	}
	if !names["praxis"] || !names["raptor"] {
		t.Errorf("tools must include praxis + raptor, got %v", names)
	}
}

func TestStatusCmd_RefreshDoesLiveFreshness(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetStatusFlags()
	statusRefresh = true
	t.Cleanup(func() { statusRefresh = false })
	origDelay := updateCheckRetryDelay
	updateCheckRetryDelay = 0
	t.Cleanup(func() { updateCheckRetryDelay = origDelay })
	withVersion(t, "dev") // praxis not checkable → only raptor fetches
	origV, origF := raptorLocalVersion, fetchRaptorTag
	t.Cleanup(func() { raptorLocalVersion, fetchRaptorTag = origV, origF })
	raptorLocalVersion = func() (string, bool) { return "0.1.0", true }
	fetched := false
	fetchRaptorTag = func() (string, error) { fetched = true; return "v0.2.0", nil }

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !fetched {
		t.Error("status --refresh must trigger a live raptor freshness fetch")
	}
}

func TestStatusCmd_NotLoggedIn_DefaultProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetStatusFlags()

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"profile": "default"`, `"profile_source": "default"`, `"logged_in": false`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull: %s", want, out)
		}
	}
}

func TestStatusCmd_LoggedIn_ReportsUsernameAndURL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetStatusFlags()

	_ = credentials.Put("default", credentials.Profile{
		URL:      "https://x.test",
		Username: "anshul@facets.cloud",
		Token:    "sk_live_t",
	})

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"logged_in": true`, `"username": "anshul@facets.cloud"`, `"url": "https://x.test"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull: %s", want, out)
		}
	}
}

func TestStatusCmd_DoesNotCallNetwork(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetStatusFlags()

	// Sentinel: if status calls fetchAuthMe, this test would deadlock /
	// fail because we set it to error.
	called := false
	orig := fetchAuthMe
	fetchAuthMe = func(string, string) (*authMeResponse, error) {
		called = true
		return nil, nil
	}
	defer func() { fetchAuthMe = orig }()

	_ = credentials.Put("default", credentials.Profile{URL: "https://x", Token: "t"})

	statusCmd.SetOut(&bytes.Buffer{})
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if called {
		t.Errorf("status must not call fetchAuthMe (it's a read-only local snapshot)")
	}
}

func TestStatusCmd_HonorsActiveProfileFromUseConfig(t *testing.T) {
	// `praxis use acme` is the documented way to switch profiles —
	// status must reflect that without any flag.
	t.Setenv("HOME", t.TempDir())
	resetStatusFlags()

	_ = credentials.Put("default", credentials.Profile{URL: "https://default.test", Token: "td"})
	_ = credentials.Put("acme", credentials.Profile{URL: "https://acme.test", Token: "ta"})
	if err := credentials.SetActive("acme"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !strings.Contains(buf.String(), `"profile": "acme"`) ||
		!strings.Contains(buf.String(), `"url": "https://acme.test"`) {
		t.Errorf("`praxis use acme` not honored, got %q", buf.String())
	}
}

// seedInstalledReceipt writes an installed.json with names duplicated
// across harnesses, so summarization (dedupe) is observable.
func seedInstalledReceipt(t *testing.T) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".praxis")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	receipt := `{
  "skills": [
    {"skill_name": "praxis", "harness": "claude-code", "path": "/h/claude/praxis/SKILL.md", "installed_at": "2026-06-12T00:00:00Z"},
    {"skill_name": "praxis", "harness": "codex", "path": "/h/codex/praxis/SKILL.md", "installed_at": "2026-06-12T00:00:00Z"},
    {"skill_name": "praxis-memory", "harness": "claude-code", "path": "/h/claude/praxis-memory/SKILL.md", "installed_at": "2026-06-12T00:00:00Z"}
  ],
  "agents": [
    {"agent_name": "praxis-auditor", "kind": "agent", "harness": "claude-code", "path": "/h/claude/agents/praxis-auditor.md", "installed_at": "2026-06-12T00:00:00Z"},
    {"agent_name": "praxis-auditor", "kind": "agent", "harness": "gemini-cli", "path": "/h/gemini/agents/praxis-auditor.md", "installed_at": "2026-06-12T00:00:00Z"}
  ]
}`
	if err := os.WriteFile(filepath.Join(dir, "installed.json"), []byte(receipt), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStatusCmd_JSONSummarizesInstalls(t *testing.T) {
	// status is read at the start of every AI conversation; the JSON
	// must stay small. Per-harness detail (paths, timestamps) lives in
	// `status --full`, `praxis agents --json`, and `list-skills --json`.
	t.Setenv("HOME", t.TempDir())
	resetStatusFlags()
	seedInstalledReceipt(t)

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	var out struct {
		Skills []string `json:"skills_installed"`
		Agents []string `json:"agents_installed"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("skills/agents should be name arrays: %v\noutput:\n%s", err, buf.String())
	}
	if want := []string{"praxis", "praxis-memory"}; !slices.Equal(out.Skills, want) {
		t.Errorf("skills_installed = %v, want deduped sorted %v", out.Skills, want)
	}
	if want := []string{"praxis-auditor"}; !slices.Equal(out.Agents, want) {
		t.Errorf("agents_installed = %v, want deduped sorted %v", out.Agents, want)
	}
}

func TestStatusCmd_EmptyReceiptMarshalsEmptyArrays(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetStatusFlags()

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"skills_installed": []`, `"agents_installed": []`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q (empty must be [], not null)\nfull: %s", want, out)
		}
	}
}

func TestStatusCmd_FullFlagIncludesDetailedEntries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetStatusFlags()
	statusFull = true
	seedInstalledReceipt(t)

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	var out struct {
		Skills []struct {
			SkillName string `json:"skill_name"`
			Harness   string `json:"harness"`
			Path      string `json:"path"`
		} `json:"skills_installed"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("--full should emit detailed objects: %v\noutput:\n%s", err, buf.String())
	}
	if len(out.Skills) != 3 || out.Skills[0].Path == "" {
		t.Errorf("--full skills_installed should be 3 detailed entries with paths, got %+v", out.Skills)
	}
}
