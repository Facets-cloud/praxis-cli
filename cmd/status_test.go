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
)

func resetStatusFlags() {
	statusJSON = false
	statusFull = false
}

func TestStatusCmd_NotLoggedIn_DefaultProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
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
	t.Setenv("PRAXIS_PROFILE", "")
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
	t.Setenv("PRAXIS_PROFILE", "")
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
	t.Setenv("PRAXIS_PROFILE", "")
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
	t.Setenv("PRAXIS_PROFILE", "")
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
	t.Setenv("PRAXIS_PROFILE", "")
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
	t.Setenv("PRAXIS_PROFILE", "")
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
