package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/httpclient"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
	return splitFields(s)
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

// The bootstrap installs the praxis skill with no login, and retires an
// embedded skill it replaced that an older praxis left behind.
func TestInstallBootstrapSkillsInstallsPraxisAndRetiresReplaced(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustMkdir(t, filepath.Join(home, ".claude"))
	old := filepath.Join(home, ".claude", "skills", "praxis-getting-started")
	mustMkdir(t, old)
	if err := os.WriteFile(filepath.Join(old, "SKILL.md"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	oldPath, _ := json.Marshal(filepath.Join(old, "SKILL.md")) // a Windows path holds backslashes
	receipt := `{"skills":[{"skill_name":"praxis-getting-started","harness":"claude-code","path":` +
		string(oldPath) + `,"installed_at":"2026-09-01T00:00:00Z"}]}`
	mustMkdir(t, filepath.Join(home, ".praxis"))
	if err := os.WriteFile(filepath.Join(home, ".praxis", "installed.json"), []byte(receipt), 0600); err != nil {
		t.Fatal(err)
	}

	n, err := installBootstrapSkills(io.Discard, true)
	if err != nil || n == 0 {
		t.Fatalf("installBootstrapSkills = %d, %v", n, err)
	}
	want, _ := skillinstall.ContentFor("praxis")
	if got, err := os.ReadFile(filepath.Join(home, ".claude", "skills", "praxis", "SKILL.md")); err != nil || string(got) != want {
		t.Errorf("praxis skill not installed: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("replaced skill still discoverable: %v", err)
	}
}

// repairPraxisHooks heals a hook wired from a version-stamped path, and must
// leave a machine that never logged in without any hooks at all.
func TestRepairPraxisHooksHealsStalePathAndAddsNone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a bin/praxis symlink without .exe, which LookPath does not find on Windows")
	}
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
	if runtime.GOOS == "windows" {
		t.Skip("uses a bin/praxis symlink without .exe, which LookPath does not find on Windows")
	}
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

// The per-command refresh runs for ordinary commands of a release build only.
func TestMaybeRefreshEmbeddedSkills(t *testing.T) {
	tests := []struct {
		version string
		args    []string
		want    bool
	}{
		{"1.16.0", []string{"mcp", "k8s_cli", "kubectl_get"}, true},
		{"1.16.0", []string{"-p", "acme", "status"}, true},
		{"1.16.0", nil, true},
		{"dev", []string{"status"}, false},
		{"1.16.0-3-gabc1234", []string{"status"}, false},
		{"1.16.0", []string{"update"}, false},
		{"1.16.0", []string{"upgrade", "--yes"}, false},
		{"1.16.0", []string{"setup"}, false},
		{"1.16.0", []string{"login"}, false},
		{"1.16.0", []string{"refresh-skills"}, false},
		{"1.16.0", []string{"git-credential", "get"}, false},
		{"1.16.0", []string{"hook", "user-prompt-submit"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.version+" "+strings.Join(tc.args, " "), func(t *testing.T) {
			origV, origR, origG := version, refreshIfStale, retireReplacedGlobals
			calls, retires := 0, 0
			version = tc.version
			refreshIfStale = func() ([]skillinstall.Installation, error) { calls++; return nil, nil }
			retireReplacedGlobals = func(func(harness.Harness, string) bool) ([]skillinstall.Installation, error) {
				retires++
				return nil, nil
			}
			t.Cleanup(func() { version, refreshIfStale, retireReplacedGlobals = origV, origR, origG })
			maybeRefreshEmbeddedSkills(tc.args)
			if (calls == 1) != tc.want || (retires == 1) != tc.want {
				t.Errorf("refresh calls = %d, retire calls = %d, want %t", calls, retires, tc.want)
			}
		})
	}
}

// Setup starts once per release version, outside CI, for ordinary commands,
// and only from the process that takes the claim.
func TestMaybeStartSetup(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		args     []string
		ci       string
		marker   string        // "" = no setup-version file
		claimAge time.Duration // 0 = no claim file
		startErr error
		want     bool
	}{
		{"fresh install", "2.1.0", []string{"status"}, "", "", 0, nil, true},
		{"after an upgrade", "2.1.0", []string{"mcp", "k8s_cli"}, "", "2.0.2", 0, nil, true},
		{"bare praxis", "2.1.0", nil, "", "2.0.2", 0, nil, true},
		{"already ran", "2.1.0", []string{"status"}, "", "2.1.0", 0, nil, false},
		{"another process started it", "2.1.0", []string{"status"}, "", "2.0.2", time.Minute, nil, false},
		{"failed setup, retry later", "2.1.0", []string{"status"}, "", "2.0.2", 59 * time.Minute, nil, false},
		{"failed setup, retry now", "2.1.0", []string{"status"}, "", "2.0.2", 2 * time.Hour, nil, true},
		{"dev build", "dev", []string{"status"}, "", "", 0, nil, false},
		{"describe build", "2.1.0-3-gabc1234", []string{"status"}, "", "", 0, nil, false},
		{"CI", "2.1.0", []string{"status"}, "true", "", 0, nil, false},
		{"prompt hook", "2.1.0", []string{"hook", "user-prompt-submit"}, "", "", 0, nil, false},
		{"ig hook", "2.1.0", []string{"ig", "hook"}, "", "", 0, nil, false},
		{"login runs raptor itself", "2.1.0", []string{"login"}, "", "", 0, nil, false},
		{"update", "2.1.0", []string{"update"}, "", "", 0, nil, false},
		{"setup itself", "2.1.0", []string{"setup"}, "", "", 0, nil, false},
		{"start fails", "2.1.0", []string{"status"}, "", "2.0.2", 0, os.ErrPermission, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			for _, k := range httpclient.CIEnvVars() {
				t.Setenv(k, "")
			}
			t.Setenv("CI", tc.ci)
			dir := filepath.Join(home, ".praxis")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			marker, claim := filepath.Join(dir, setupVersionFile), filepath.Join(dir, setupClaimFile)
			if tc.marker != "" {
				if err := os.WriteFile(marker, []byte(tc.marker+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.claimAge > 0 {
				if err := os.WriteFile(claim, nil, 0o644); err != nil {
					t.Fatal(err)
				}
				old := time.Now().Add(-tc.claimAge)
				if err := os.Chtimes(claim, old, old); err != nil {
					t.Fatal(err)
				}
			}
			origV, origS := version, startSetup
			starts := 0
			version = tc.version
			startSetup = func(string) error { starts++; return tc.startErr }
			t.Cleanup(func() { version, startSetup = origV, origS })

			maybeStartSetup(tc.args)

			if (starts == 1) != tc.want {
				t.Fatalf("starts = %d, want started %t", starts, tc.want)
			}
			// Only the setup process records the version.
			b, _ := os.ReadFile(marker)
			if got := strings.TrimSpace(string(b)); got != tc.marker {
				t.Errorf("setup-version = %q, want %q", got, tc.marker)
			}
			info, err := os.Stat(claim)
			switch {
			case tc.want && tc.startErr == nil:
				if err != nil || time.Since(info.ModTime()) > time.Minute {
					t.Errorf("want a fresh claim, got %v", err)
				}
			case tc.want:
				if err == nil {
					t.Error("a failed start must release the claim")
				}
			case tc.claimAge == 0 && err == nil:
				t.Error("a skipped command must not take the claim")
			}
		})
	}
}

// The background setup gets its own log, and the caller does not wait for it.
func TestStartBackgroundSetup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".praxis")
	orig := setupExecutable
	t.Cleanup(func() { setupExecutable = orig })

	setupExecutable = func() (string, error) { return trueBinary(t), nil }
	if err := startBackgroundSetup(dir); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "setup.log")); err != nil {
		t.Errorf("setup.log: %v", err)
	}

	setupExecutable = func() (string, error) { return filepath.Join(dir, "missing"), nil }
	// Windows reports a missing program as exec.ErrNotFound.
	if err := startBackgroundSetup(dir); !errors.Is(err, os.ErrNotExist) && !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("missing binary: err = %v, want ErrNotExist", err)
	}
}

// A successful setup records its version and releases the claim.
func TestSetupRecordsVersion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if !claimSetup(filepath.Join(home, ".praxis", setupClaimFile), time.Now()) {
		t.Fatal("claim")
	}
	origV := version
	version = "2.1.0"
	t.Cleanup(func() { version = origV })
	var out bytes.Buffer
	setupCmd.SetOut(&out)
	t.Cleanup(func() { setupCmd.SetOut(nil) })

	if err := setupCmd.RunE(setupCmd, nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".praxis", setupVersionFile))
	if err != nil || strings.TrimSpace(string(b)) != "2.1.0" {
		t.Errorf("setup-version = %q, %v; want 2.1.0", b, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".praxis", setupClaimFile)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("claim after a successful setup: %v, want removed", err)
	}
}

// trueBinary is a program that starts and exits at once on this platform.
func trueBinary(t *testing.T) string {
	if runtime.GOOS == "windows" {
		p, err := exec.LookPath("whoami.exe")
		if err != nil {
			t.Skip("whoami.exe not found")
		}
		return p
	}
	return "/usr/bin/true"
}
