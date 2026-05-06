package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
)

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

func TestSkillInstall_NoArg_DefaultsToPraxis(t *testing.T) {
	var capturedName string
	withSeams(t,
		func() []harness.Harness { return []harness.Harness{{Name: "claude-code", Detected: true}} },
		func(name string, hosts []harness.Harness) ([]skillinstall.Installation, error) {
			capturedName = name
			return []skillinstall.Installation{{SkillName: name, Harness: "claude-code", Path: "/p"}}, nil
		}, nil, nil)

	skillInstallCmd.SetOut(&bytes.Buffer{})
	if err := skillInstallCmd.RunE(skillInstallCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if capturedName != "praxis" {
		t.Errorf("install called with name %q, want %q", capturedName, defaultSkillName)
	}
}

func TestSkillInstall_NoHosts(t *testing.T) {
	withSeams(t, func() []harness.Harness { return nil }, nil, nil, nil)

	var buf bytes.Buffer
	skillInstallCmd.SetOut(&buf)
	if err := skillInstallCmd.RunE(skillInstallCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !strings.Contains(buf.String(), "No supported AI hosts detected") {
		t.Errorf("output = %q, want substring 'No supported AI hosts detected'", buf.String())
	}
}

func TestSkillInstall_Success(t *testing.T) {
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
	skillInstallCmd.SetOut(&buf)
	if err := skillInstallCmd.RunE(skillInstallCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := buf.String()
	for _, want := range []string{"claude-code", "codex", "/fake/claude-code/praxis/SKILL.md", "Installed \"praxis\" into 2 host(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestSkillInstall_PropagatesError(t *testing.T) {
	withSeams(t,
		func() []harness.Harness {
			return []harness.Harness{{Name: "claude-code", Detected: true}}
		},
		func(string, []harness.Harness) ([]skillinstall.Installation, error) {
			return nil, errors.New("disk full")
		},
		nil, nil)

	skillInstallCmd.SetOut(&bytes.Buffer{})
	err := skillInstallCmd.RunE(skillInstallCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("err = %v, want substring 'disk full'", err)
	}
}

func TestSkillUninstall_NothingFound(t *testing.T) {
	withSeams(t, nil, nil,
		func(string) ([]skillinstall.Installation, error) {
			return nil, nil
		},
		nil)

	var buf bytes.Buffer
	skillUninstallCmd.SetOut(&buf)
	if err := skillUninstallCmd.RunE(skillUninstallCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !strings.Contains(buf.String(), "No installations of \"praxis\"") {
		t.Errorf("output = %q, want substring 'No installations'", buf.String())
	}
}

func TestSkillUninstall_RemovesAndReports(t *testing.T) {
	withSeams(t, nil, nil,
		func(name string) ([]skillinstall.Installation, error) {
			return []skillinstall.Installation{
				{SkillName: name, Harness: "claude-code", Path: "/c"},
				{SkillName: name, Harness: "codex", Path: "/x"},
			}, nil
		},
		nil)

	var buf bytes.Buffer
	skillUninstallCmd.SetOut(&buf)
	if err := skillUninstallCmd.RunE(skillUninstallCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := buf.String()
	for _, want := range []string{"claude-code", "codex", "/c", "/x", "Uninstalled \"praxis\" from 2 host(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, out)
		}
	}
}

func TestSkillListInstalled_Empty(t *testing.T) {
	withSeams(t, nil, nil, nil,
		func() ([]skillinstall.Installation, error) { return nil, nil })

	var buf bytes.Buffer
	skillListInstalledCmd.SetOut(&buf)
	if err := skillListInstalledCmd.RunE(skillListInstalledCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !strings.Contains(buf.String(), "No skills installed") {
		t.Errorf("output = %q, want substring 'No skills installed'", buf.String())
	}
}

func TestSkillListInstalled_Populated(t *testing.T) {
	withSeams(t, nil, nil, nil,
		func() ([]skillinstall.Installation, error) {
			return []skillinstall.Installation{
				{SkillName: "praxis", Harness: "claude-code", Path: "/p1"},
				{SkillName: "praxis", Harness: "codex", Path: "/p2"},
			}, nil
		})

	var buf bytes.Buffer
	skillListInstalledCmd.SetOut(&buf)
	if err := skillListInstalledCmd.RunE(skillListInstalledCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := buf.String()
	for _, want := range []string{"SKILL", "HARNESS", "PATH", "praxis", "claude-code", "codex", "/p1", "/p2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, out)
		}
	}
}
