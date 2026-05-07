package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
)

func resetInitFlags() {
	initJSON = false
}

func TestInitCmd_NoHostsDetected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetInitFlags()

	withSeams(t,
		func() []harness.Harness { return nil }, // no hosts
		nil, nil, nil,
	)

	var buf bytes.Buffer
	initCmd.SetOut(&buf)
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"hosts_detected": null`) && !strings.Contains(out, `"hosts_detected": []`) {
		t.Errorf("expected empty hosts_detected, got %q", out)
	}
	if !strings.Contains(out, `"logged_in": false`) {
		t.Errorf("not logged in → expected logged_in: false, got %q", out)
	}
	if !strings.Contains(out, "praxis login") {
		t.Errorf("expected next-steps to mention praxis login, got %q", out)
	}
}

func TestInitCmd_HostsDetected_InstallSkillCalled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetInitFlags()

	withSeams(t,
		func() []harness.Harness {
			return []harness.Harness{
				{Name: "claude-code", Detected: true, SkillDir: "/fake/c"},
				{Name: "codex", Detected: true, SkillDir: "/fake/x"},
			}
		},
		func(name string, hosts []harness.Harness) ([]skillinstall.Installation, error) {
			out := make([]skillinstall.Installation, 0, len(hosts))
			for _, h := range hosts {
				out = append(out, skillinstall.Installation{
					SkillName:   name,
					Harness:     h.Name,
					Path:        h.SkillDir + "/" + name + "/SKILL.md",
					InstalledAt: time.Now(),
				})
			}
			return out, nil
		},
		nil, nil,
	)

	var buf bytes.Buffer
	initCmd.SetOut(&buf)
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"claude-code"`, `"codex"`, `"hosts_detected"`, `"skills_installed"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull: %s", want, out)
		}
	}
}

func TestInitCmd_LoggedIn_NoNextSteps(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetInitFlags()

	_ = credentials.Put("default", credentials.Profile{
		URL: "https://x.test", Token: "sk_live", Username: "u@x",
	})

	withSeams(t,
		func() []harness.Harness { return nil },
		nil, nil, nil,
	)

	var buf bytes.Buffer
	initCmd.SetOut(&buf)
	if err := initCmd.RunE(initCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"logged_in": true`) {
		t.Errorf("expected logged_in: true, got %q", out)
	}
	if strings.Contains(out, "praxis login") {
		t.Errorf("logged-in profile should NOT trigger 'praxis login' next-step, got %q", out)
	}
}
