package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/raptorinstall"
	"github.com/Facets-cloud/praxis-cli/internal/render"
)

var ensureRaptorBinary = raptorinstall.Ensure
var updateRaptor = runRaptorUpgrade
var installRaptorSkills = runInstallRaptorSkills

type raptorUpgradeResult struct {
	Attempted bool                 `json:"attempted"`
	Completed bool                 `json:"completed"`
	Binary    raptorinstall.Result `json:"binary"`
	Output    string               `json:"output,omitempty"`
	Warning   string               `json:"warning,omitempty"`
}

// runRaptorUpgrade installs a missing raptor, then runs its own upgrade. JSON
// callers get raptor's output captured so it cannot corrupt the envelope.
func runRaptorUpgrade(out io.Writer, asJSON, yes bool) (result raptorUpgradeResult, err error) {
	result.Binary, err = ensureRaptorBinary()
	if err != nil {
		result.Warning = err.Error()
		return
	}
	// raptor compares versions as strings, so it would replace a local build.
	if raptorIsDevBuild(result.Binary.Path) {
		result.Warning = "Raptor at " + result.Binary.Path + " is a development build; not upgraded"
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	args := []string{"upgrade"}
	if yes {
		args = append(args, "--yes")
	}
	cmd := exec.CommandContext(ctx, result.Binary.Path, args...)
	var captured bytes.Buffer
	if asJSON {
		cmd.Stdout = &captured
		cmd.Stderr = &captured
	} else {
		cmd.Stdout = out
		cmd.Stderr = out
		cmd.Stdin = os.Stdin
	}
	result.Attempted = true
	err = cmd.Run()
	result.Output = strings.TrimSpace(captured.String())
	result.Completed = err == nil
	if err != nil {
		err = fmt.Errorf("raptor upgrade failed: %w", err)
		result.Warning = err.Error()
	}
	return
}

// raptorIsDevBuild is true only when raptor answers without a release version.
// No answer is not proof, so the upgrade still runs.
func raptorIsDevBuild(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	return err == nil && !raptorSemver.Match(out)
}

// finishToolUpdate runs raptor's upgrade after praxis's own outcome is known,
// so `praxis update` updates both CLIs and reports each result separately.
func finishToolUpdate(out io.Writer, asJSON, yes bool, payload map[string]any) error {
	raptor, err := updateRaptor(out, asJSON, yes)
	if asJSON {
		payload["raptor"] = raptor
		return errors.Join(err, render.JSON(out, payload))
	}
	if raptor.Binary.Installed {
		fmt.Fprintf(out, "Installed Raptor CLI at %s\n", raptor.Binary.Path)
	}
	if raptor.Binary.PathWarning != "" {
		fmt.Fprintln(out, raptor.Binary.PathWarning)
	}
	return err
}

// prepareRaptor installs raptor when it is missing. Failure is a warning: the
// caller's own work (login, setup) must still complete.
func prepareRaptor(out io.Writer, asJSON bool) (raptorinstall.Result, error) {
	result, err := ensureRaptorBinary()
	if !asJSON {
		if err != nil {
			fmt.Fprintf(out, "Warning: Raptor installation: %v\n", err)
		}
		if result.Installed {
			fmt.Fprintf(out, "Installed Raptor CLI at %s\n", result.Path)
		}
		if result.PathWarning != "" {
			fmt.Fprintln(out, result.PathWarning)
		}
	}
	return result, err
}

// raptorAgents maps a praxis host to raptor's --agent name. Antigravity has
// no raptor agent yet, so it gets no raptor skill.
var raptorAgents = map[string]string{"claude-code": "claude", "codex": "codex", "gemini-cli": "gemini"}

// minRaptorSkillVersion is the first raptor that ships the one raptor skill.
const minRaptorSkillVersion = "0.1.107"

// runInstallRaptorSkills asks raptor to install its own skill for each host.
// raptor then registers the path and rewrites the skill after every upgrade,
// so praxis never owns a copy that can go stale. projectDir is "" for a
// user-level install. A host that already reads a raptor skill from the shared
// ~/.agents/skills root is left alone, so it never sees two.
func runInstallRaptorSkills(raptor string, hosts []harness.Harness, projectDir string) ([]skillInstallationLite, error) {
	if raptor == "" {
		return nil, errors.New("raptor CLI is missing, so its skill was not installed")
	}
	base := projectDir
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		base = home
	}
	if v := raptorVersionAt(raptor); v != "" && compareSemver(v, minRaptorSkillVersion) < 0 {
		return nil, fmt.Errorf("raptor %s is too old to install its skill; run `praxis update`", v)
	}
	var installed []skillInstallationLite
	var errs []error
	done := map[string]bool{}
	for _, h := range hosts {
		agent, ok := raptorAgents[h.Name]
		if !ok || done[agent] {
			continue
		}
		done[agent] = true
		native := filepath.Join(base, "."+agent, "skills")
		if h.SkillDir != native {
			if _, err := os.Stat(filepath.Join(h.SkillDir, "raptor", "SKILL.md")); err == nil {
				continue
			}
		}
		args := []string{"install", "skill", "--agent", agent}
		if projectDir != "" {
			args = append(args, "--path", projectDir)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, raptor, args...)
		cmd.Dir = base
		cmd.Env = append(os.Environ(), "RAPTOR_NO_UPDATE_CHECK=1")
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("raptor skill for %s: %w: %s", h.Name, err, strings.TrimSpace(string(out))))
			continue
		}
		installed = append(installed, skillInstallationLite{Harness: h.Name, Path: filepath.Join(native, "raptor", "SKILL.md")})
	}
	return installed, errors.Join(errs...)
}

// raptorVersionAt is the release version of the raptor at path, or "" for a
// development build or no answer.
func raptorVersionAt(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return ""
	}
	return raptorSemver.FindString(string(out))
}

// reportRaptorSkills prints where raptor installed its skill, then any failure.
func reportRaptorSkills(out io.Writer, installed []skillInstallationLite, err error) {
	for _, in := range installed {
		fmt.Fprintf(out, "  ✓ raptor skill  %-12s @ %s\n", in.Harness, in.Path)
	}
	if err != nil {
		fmt.Fprintf(out, "Warning: %v\n", err)
	}
}
