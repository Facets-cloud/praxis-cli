package cmd

import (
	"io"
	"os"
	"path/filepath"
	"testing"
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
