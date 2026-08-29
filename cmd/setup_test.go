package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/claudehooks"
)

func TestSetupCommandStaysHidden(t *testing.T) {
	// setup is cask/first-run plumbing, not user surface — the documented
	// command list (root_test.go) must not grow, and `init` must stay gone.
	if !setupCmd.Hidden {
		t.Error("setup must remain Hidden")
	}
}

func TestFirstRunSkipped(t *testing.T) {
	skip := map[string]bool{
		// machine-invoked / self-referential → skip
		"ig hook session-start": true,
		"ig list":               true,
		"mcp k8s_cli run":       true,
		"completion zsh":        true,
		"__complete":            true,
		"git-credential get":    true,
		"setup":                 true,
		"version":               true,
		"update":                true,
		// value-taking flag before the command must not misclassify
		"--profile prod ig hook session-start": true,
		"--profile=prod mcp k8s_cli run":       true,
		// human GTM entry points → bootstrap
		"status --json":         false,
		"login --url https://x": false,
		"list-skills":           false,
		"--profile prod status": false,
		"":                      false, // bare `praxis`
		"--help":                false, // flags-only
	}
	for cmdline, want := range skip {
		args := splitArgs(cmdline)
		if got := firstRunSkipped(args); got != want {
			t.Errorf("firstRunSkipped(%q) = %v, want %v", cmdline, got, want)
		}
	}
}

func splitArgs(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, f := range splitFields(s) {
		out = append(out, f)
	}
	return out
}

func splitFields(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func TestFirstRunBootstrapGating(t *testing.T) {
	marker := filepath.Join(t.TempDir(), ".bootstrap-v1")
	calls := 0
	install := func() (int, error) { calls++; return 4, nil } // 4 host installs

	// Machine command → never installs.
	if firstRunBootstrap([]string{"ig", "hook", "session-start"}, marker, install) {
		t.Error("machine command must not bootstrap")
	}
	if calls != 0 {
		t.Fatalf("machine command must not call install, got %d", calls)
	}

	// Human command, marker absent → installs + writes marker.
	if !firstRunBootstrap([]string{"status"}, marker, install) {
		t.Error("human command with no marker must bootstrap")
	}
	if calls != 1 {
		t.Fatalf("expected 1 install, got %d", calls)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker must be written after a successful bootstrap: %v", err)
	}

	// Marker present → no re-install.
	if firstRunBootstrap([]string{"status"}, marker, install) {
		t.Error("marker present must not re-bootstrap")
	}
	if calls != 1 {
		t.Errorf("must not re-install when marker present, got %d calls", calls)
	}
}

func TestFirstRunBootstrapFailureIsRetryable(t *testing.T) {
	marker := filepath.Join(t.TempDir(), ".bootstrap-v1")
	failing := func() (int, error) { return 0, io.ErrUnexpectedEOF }

	if firstRunBootstrap([]string{"status"}, marker, failing) {
		t.Error("a failed install must report false, not block")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a failed install must NOT write the marker (so it retries)")
	}
}

// No AI host yet (n == 0) must NOT write the marker, else installing a host
// later would be permanently skipped by first-run.
func TestFirstRunBootstrapNoHostStaysRetryable(t *testing.T) {
	marker := filepath.Join(t.TempDir(), ".bootstrap-v1")
	noHost := func() (int, error) { return 0, nil }

	if firstRunBootstrap([]string{"status"}, marker, noHost) {
		t.Error("no-host bootstrap must report false")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("no-host bootstrap must NOT write the marker (retry when a host appears)")
	}
}

func TestInstallBootstrapSkillsWritesGTMSkill(t *testing.T) {
	// Redirect HOME so the install (and its receipt) land in a temp tree.
	home := t.TempDir()
	t.Setenv("HOME", home)
	n, err := installBootstrapSkills(io.Discard, true)
	if err != nil {
		t.Fatalf("install must not error: %v", err)
	}
	if n == 0 {
		t.Skip("no AI hosts detected on this machine — nothing to assert")
	}
	// The getting-started SKILL.md must be written with GTM content, no login.
	found := false
	_ = filepath.Walk(home, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return nil
		}
		if filepath.Base(filepath.Dir(p)) == "praxis-getting-started" && filepath.Base(p) == "SKILL.md" {
			b, _ := os.ReadFile(p)
			if bodyHas(string(b), "Praxis by Facets") && bodyHas(string(b), "facets.cloud/signup") {
				found = true
			}
		}
		return nil
	})
	if !found {
		t.Error("getting-started SKILL.md with GTM content was not installed into any host")
	}
}

func bodyHas(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// repairPraxisHooks heals a hook wired from a version-stamped path, and must
// leave a machine that never logged in without any hooks at all.
func TestRepairPraxisHooksHealsStalePathAndAddsNone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(binDir, "praxis")
	if err := os.WriteFile(self, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Cleanup(claudehooks.SetExecPathForTest(self))

	// A machine with no praxis hooks stays untouched.
	if repaired, warn := repairPraxisHooks(); len(repaired) != 0 || warn != "" {
		t.Fatalf("unwired machine: repaired=%v warn=%q, want no change", repaired, warn)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("repair created a settings file on an unwired machine")
	}

	// A hook wired from a Caskroom path the upgrade deleted is re-pointed.
	host, _ := claudehooks.ByHarness(home, "claude-code")
	if _, err := claudehooks.Install(host, "/opt/homebrew/Caskroom/praxis/1.6.0/praxis_darwin_arm64"); err != nil {
		t.Fatal(err)
	}
	repaired, warn := repairPraxisHooks()
	if warn != "" || len(repaired) != 1 || repaired[0] != "claude-code" {
		t.Fatalf("repaired=%v warn=%q, want [claude-code] and no warning", repaired, warn)
	}
	raw, err := os.ReadFile(host.File)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Caskroom") || !strings.Contains(string(raw), self) {
		t.Fatalf("hook not re-pointed at %s:\n%s", self, raw)
	}

	// Already current: nothing to report.
	if repaired, _ := repairPraxisHooks(); len(repaired) != 0 {
		t.Fatalf("second run repaired %v, want nothing", repaired)
	}
}

// An unparseable settings.json must surface, not vanish.
func TestRepairPraxisHooksReportsUnparseableSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(binDir, "praxis")
	if err := os.WriteFile(self, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Cleanup(claudehooks.SetExecPathForTest(self))

	host, _ := claudehooks.ByHarness(home, "claude-code")
	if err := os.MkdirAll(filepath.Dir(host.File), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(host.File, []byte("{ not json,"), 0o600); err != nil {
		t.Fatal(err)
	}
	repaired, warn := repairPraxisHooks()
	if warn == "" || !strings.Contains(warn, "not valid JSON") {
		t.Fatalf("warning = %q, want the invalid-JSON error", warn)
	}
	if len(repaired) != 0 {
		t.Fatalf("repaired = %v, want nothing", repaired)
	}
	var buf bytes.Buffer
	printHookRepair(&buf, false, repaired, warn)
	if !strings.Contains(buf.String(), "⚠") {
		t.Fatalf("printHookRepair stayed silent on a warning: %q", buf.String())
	}
	if buf.Reset(); func() string { printHookRepair(&buf, true, repaired, warn); return buf.String() }() != "" {
		t.Fatalf("printHookRepair printed under --json")
	}
}
