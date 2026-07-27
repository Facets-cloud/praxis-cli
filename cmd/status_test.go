package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/raptorstate"
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
	// Seed a FRESH raptor cache entry: without this, freshCachedOrFetch would
	// also fetch, so the test would pass even if --refresh stopped using
	// freshLive. A fresh entry is only bypassed by a genuine live check.
	if err := saveFreshnessCache(freshnessCache{
		"raptor": {CheckedAt: time.Now(), LatestVersion: "v0.1.0"},
	}); err != nil {
		t.Fatal(err)
	}
	fetched := false
	fetchRaptorTag = func() (string, error) { fetched = true; return "v0.2.0", nil }

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !fetched {
		t.Error("status --refresh must trigger a live raptor freshness fetch (freshLive bypasses a fresh cache)")
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
	fetchAuthMe = func(string, map[string]string) (*authMeResponse, error) {
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

// ─── raptor auth-state block ─────────────────────────────────────────────

// isolateRaptorEnv clears every env var the raptor resolver consults so the
// developer's shell (or CI) can't leak a profile into the test.
func isolateRaptorEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"CONTROL_PLANE_URL", "FACETS_USERNAME", "FACETS_TOKEN", "FACETS_PROFILE"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

// writeRaptorCreds drops a raptor-style credentials file into the fake HOME.
func writeRaptorCreds(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".facets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// stubStatusFreshness keeps the freshness engine off the network for status
// tests that aren't about freshness.
func stubStatusFreshness(t *testing.T) {
	t.Helper()
	withVersion(t, "dev")
	origV := raptorLocalVersion
	t.Cleanup(func() { raptorLocalVersion = origV })
	raptorLocalVersion = func() (string, bool) { return "0.1.0", true }
}

func TestStatusCmd_RaptorBlock_MatchingDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	isolateRaptorEnv(t)
	resetStatusFlags()
	stubStatusFreshness(t)

	if err := credentials.Put("default", credentials.Profile{URL: "https://root.test", Username: "u@x", Token: "tok"}); err != nil {
		t.Fatal(err)
	}
	writeRaptorCreds(t, "[default]\ncontrol_plane_url = https://root.test\nusername = u@x\ntoken = pat\n")

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	var s map[string]any
	if err := json.Unmarshal(buf.Bytes(), &s); err != nil {
		t.Fatalf("status not JSON: %v\n%s", err, buf.String())
	}
	rb, ok := s["raptor"].(map[string]any)
	if !ok {
		t.Fatalf("raptor block missing: %v", s["raptor"])
	}
	for k, want := range map[string]any{
		"found":              true,
		"pinned":             false,
		"profile":            "default",
		"source":             "default",
		"control_plane_url":  "https://root.test",
		"matches_praxis_url": true,
	} {
		if got := rb[k]; got != want {
			t.Errorf("raptor[%q] = %v, want %v", k, got, want)
		}
	}
}

func TestStatusCmd_RaptorBlock_PinnedMismatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	isolateRaptorEnv(t)
	resetStatusFlags()
	stubStatusFreshness(t)

	if err := credentials.Put("default", credentials.Profile{
		URL: "https://root.test", Username: "u@x", Token: "tok", RaptorProfile: "acme",
	}); err != nil {
		t.Fatal(err)
	}
	writeRaptorCreds(t, "[acme]\ncontrol_plane_url = https://acme.test\nusername = u@x\ntoken = pat\n")

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	var s map[string]any
	if err := json.Unmarshal(buf.Bytes(), &s); err != nil {
		t.Fatalf("status not JSON: %v\n%s", err, buf.String())
	}
	rb, _ := s["raptor"].(map[string]any)
	if rb == nil {
		t.Fatalf("raptor block missing")
	}
	for k, want := range map[string]any{
		"pinned":             true,
		"profile":            "acme",
		"source":             "pin",
		"matches_praxis_url": false,
	} {
		if got := rb[k]; got != want {
			t.Errorf("raptor[%q] = %v, want %v", k, got, want)
		}
	}
}

func TestStatusCmd_RaptorBlock_NothingResolved(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	isolateRaptorEnv(t)
	resetStatusFlags()
	stubStatusFreshness(t)

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)
	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	var s map[string]any
	if err := json.Unmarshal(buf.Bytes(), &s); err != nil {
		t.Fatalf("status not JSON: %v\n%s", err, buf.String())
	}
	rb, _ := s["raptor"].(map[string]any)
	if rb == nil {
		t.Fatalf("raptor block missing")
	}
	if rb["found"] != false {
		t.Errorf("raptor.found = %v, want false", rb["found"])
	}
	if _, has := rb["matches_praxis_url"]; has {
		t.Error("matches_praxis_url must be omitted when no raptor profile resolved")
	}
}

func TestRaptorStatusLine(t *testing.T) {
	tests := []struct {
		name string
		st   raptorstate.State
		url  string
		want string
	}{
		{
			name: "found and matching",
			st:   raptorstate.State{Found: true, Profile: "default", Source: raptorstate.SourceDefault, ControlPlaneURL: "https://root.test"},
			url:  "https://root.test",
			want: "profile default (default) → https://root.test (matches praxis url: yes)",
		},
		{
			name: "found and mismatched",
			st:   raptorstate.State{Found: true, Profile: "acme", Source: raptorstate.SourceEnvProfile, ControlPlaneURL: "https://acme.test"},
			url:  "https://root.test",
			want: "profile acme (env-profile) → https://acme.test (matches praxis url: no)",
		},
		{
			name: "pinned but missing",
			st:   raptorstate.State{Pinned: true, Profile: "ghost", Source: raptorstate.SourcePin},
			want: "pinned profile \"ghost\" not found in ~/.facets/credentials — run `raptor login`",
		},
		{
			name: "env profile missing",
			st:   raptorstate.State{Profile: "ghost", Source: raptorstate.SourceEnvProfile},
			want: "profile \"ghost\" (env-profile) not found in ~/.facets/credentials",
		},
		{
			// States the fact AND the next step — nothing else in the repo
			// tells a user where to get raptor.
			name: "not installed",
			st:   raptorstate.State{},
			want: "not installed — get it at " + raptorInstallURL,
		},
		{
			name: "installed, nothing resolved",
			st:   raptorstate.State{Installed: true},
			want: "no profile resolved — run `raptor login`",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := raptorStatusLine(tt.st, tt.url); got != tt.want {
				t.Errorf("raptorStatusLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A new user installs praxis, runs `praxis login`, and sees a clean result —
// but raptor is the CLI that actually reaches the Facets control plane
// (projects, resources, environments, releases). #68 made status say
// "not installed", which is the right fact but not an actionable one: it names
// no consequence and no next step. These tests pin the actionable form.
func TestRaptorStatusLine_NotInstalledPointsAtTheInstall(t *testing.T) {
	got := raptorStatusLine(raptorstate.State{}, "https://root.test")
	if !strings.Contains(got, "not installed") {
		t.Errorf("line must still state the fact; got %q", got)
	}
	if !strings.Contains(got, raptorInstallURL) {
		t.Errorf("line must point at where to get raptor; got %q", got)
	}
}

// setupNotice is the closing summary. Without it the `raptor: not installed`
// line is followed by `logged in: yes`, so the output as a whole still reads
// healthy and the user has no reason to look closer.
func TestSetupNotice(t *testing.T) {
	tests := []struct {
		name      string
		st        raptorstate.State
		wantEmpty bool
		must      []string
	}{
		{
			name: "not installed — needs install AND login",
			st:   raptorstate.State{},
			must: []string{"setup incomplete", "not installed", raptorInstallURL, "raptor login"},
		},
		{
			name: "installed but nothing resolved — needs login only",
			st:   raptorstate.State{Installed: true},
			must: []string{"setup incomplete", "raptor login"},
		},
		{
			name: "installed, pinned profile missing — needs login",
			st:   raptorstate.State{Installed: true, Pinned: true, Profile: "ghost", Source: raptorstate.SourcePin},
			must: []string{"setup incomplete", "raptor login"},
		},
		{
			name:      "fully set up — stay quiet",
			st:        raptorstate.State{Installed: true, Found: true, Profile: "default", ControlPlaneURL: "https://root.test"},
			wantEmpty: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := setupNotice(tt.st)
			if tt.wantEmpty {
				if got != "" {
					t.Errorf("want no notice when setup is complete; got %q", got)
				}
				return
			}
			if got == "" {
				t.Fatal("want a notice, got none")
			}
			for _, want := range tt.must {
				if !strings.Contains(got, want) {
					t.Errorf("notice missing %q; got:\n%s", want, got)
				}
			}
		})
	}
}

// An installed-but-not-logged-in raptor must NOT be told to install again —
// that sends the user down the wrong path.
func TestSetupNotice_InstalledDoesNotSuggestInstalling(t *testing.T) {
	got := setupNotice(raptorstate.State{Installed: true})
	if strings.Contains(got, raptorInstallURL) {
		t.Errorf("raptor is already installed; notice must not point at the install URL:\n%s", got)
	}
}

func TestRaptorAssetName(t *testing.T) {
	// Verified against the real assets on Facets-cloud/raptor-releases
	// (v0.1.91 publishes darwin/linux, amd64/arm64 only).
	for _, tt := range []struct{ goos, goarch, want string }{
		{"darwin", "arm64", "raptor-darwin-arm64"},
		{"darwin", "amd64", "raptor-darwin-amd64"},
		{"linux", "amd64", "raptor-linux-amd64"},
		{"linux", "arm64", "raptor-linux-arm64"},
		{"windows", "amd64", ""}, // not published — must not invent a URL
		{"linux", "386", ""},
	} {
		if got := raptorAssetName(tt.goos, tt.goarch); got != tt.want {
			t.Errorf("raptorAssetName(%q,%q) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
		}
	}
}

// The install hint rides inside the existing `raptor` block from #68 rather
// than as a parallel top-level key, so the meta-skill's "act on the raptor
// block" contract keeps working.
//
// `docs` is the PRIMARY answer: raptor's own README owns the install steps and
// we must not fork them (it already drifts — it documents Windows binaries the
// releases don't publish). `no_sudo_commands` is an explicit escape hatch for
// non-interactive hosts that cannot answer raptor's documented `sudo mv`.
func TestRaptorStatusBlock_InstallHint(t *testing.T) {
	t.Run("absent: README is the primary pointer", func(t *testing.T) {
		b := raptorStatusBlockFor(raptorstate.State{}, "https://x.test", "darwin", "arm64")
		hint, _ := b["install_hint"].(map[string]any)
		if hint == nil {
			t.Fatal("install_hint missing when raptor is not installed")
		}
		docs, _ := hint["docs"].(string)
		if !strings.Contains(docs, "raptor-releases") {
			t.Errorf("docs must point at raptor's own install instructions, got %q", docs)
		}
		note, _ := hint["note"].(string)
		if !strings.Contains(note, "sudo") || !strings.Contains(note, "PATH") {
			t.Errorf("note must say the official steps use sudo and that ~/.local/bin needs to be on PATH; got %q", note)
		}
	})

	t.Run("hatch names this machine's asset and needs no sudo", func(t *testing.T) {
		b := raptorStatusBlockFor(raptorstate.State{}, "https://x.test", "darwin", "arm64")
		hint, _ := b["install_hint"].(map[string]any)
		if !strings.Contains(hint["asset_url"].(string), "raptor-darwin-arm64") {
			t.Errorf("asset_url must name this machine's build, got %v", hint["asset_url"])
		}
		cmds := strings.Join(toStrings(hint["no_sudo_commands"]), "\n")
		// sudo prompts for a password and hangs a non-interactive AI host.
		if strings.Contains(cmds, "sudo") {
			t.Errorf("the hatch exists to avoid sudo:\n%s", cmds)
		}
		if !strings.Contains(cmds, "chmod +x") {
			t.Errorf("downloaded binary must be made executable:\n%s", cmds)
		}
	})

	t.Run("installed: no hint", func(t *testing.T) {
		b := raptorStatusBlockFor(raptorstate.State{Installed: true}, "https://x.test", "darwin", "arm64")
		if _, has := b["install_hint"]; has {
			t.Error("install_hint must be omitted once raptor is installed")
		}
	})

	t.Run("unpublished platform: docs only, no fabricated url", func(t *testing.T) {
		b := raptorStatusBlockFor(raptorstate.State{}, "https://x.test", "windows", "amd64")
		hint, _ := b["install_hint"].(map[string]any)
		if hint == nil {
			t.Fatal("install_hint missing")
		}
		if _, has := hint["asset_url"]; has {
			t.Error("must not fabricate a download URL for a platform the releases don't publish")
		}
		if _, has := hint["no_sudo_commands"]; has {
			t.Error("no hatch without a real asset — send them to docs")
		}
		if hint["docs"] == nil {
			t.Error("docs must always be present")
		}
	})

	// #68's fields must survive untouched.
	t.Run("preserves the #68 block", func(t *testing.T) {
		b := raptorStatusBlockFor(raptorstate.State{Installed: true, Found: true,
			Profile: "default", ControlPlaneURL: "https://x.test"}, "https://x.test", "darwin", "arm64")
		for _, k := range []string{"installed", "found", "pinned", "control_plane_url", "matches_praxis_url"} {
			if _, has := b[k]; !has {
				t.Errorf("#68 field %q went missing", k)
			}
		}
	})
}

func toStrings(v any) []string {
	out, _ := v.([]string)
	return out
}
