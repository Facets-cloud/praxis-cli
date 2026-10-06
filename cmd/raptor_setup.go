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
	var skills []skillInstallationLite
	var skillErr error
	if raptor.Completed {
		skills, skillErr = refreshRaptorSkills(out, asJSON)
	}
	if asJSON {
		payload["raptor"] = raptor
		payload["raptor_skills"] = skills
		if skillErr != nil {
			payload["raptor_skill_warning"] = skillErr.Error()
		}
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

// runInstallRaptorSkills asks raptor to install its own skill, at user level,
// for each host. raptor then registers the path and rewrites the skill after
// every upgrade, so praxis never owns a copy that can go stale. The skill does
// not depend on the praxis profile, so a project-scoped login installs it at
// user level too: a project copy would only give the host a second one. A host
// that already reads a raptor skill from the shared ~/.agents/skills root is
// left alone for the same reason. hosts must be the user-level hosts.
func runInstallRaptorSkills(raptor string, hosts []harness.Harness) ([]skillInstallationLite, error) {
	if raptor == "" {
		return nil, errors.New("raptor CLI is missing, so its skill was not installed")
	}
	base, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	v, err := raptorVersionAt(raptor)
	if err != nil {
		return nil, fmt.Errorf("raptor at %s did not answer --version, so its skill was not installed: %w", raptor, err)
	}
	if v != "" && compareSemver(v, minRaptorSkillVersion) < 0 {
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
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, raptor, "install", "skill", "--agent", agent)
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
// development build. An error means raptor did not answer.
func raptorVersionAt(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", err
	}
	return raptorSemver.FindString(string(out)), nil
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

// refreshRaptorSkills reinstalls the raptor skill with the raptor now on PATH,
// so an upgrade reaches the skill at once rather than at raptor's next run.
// It resolves raptor again: an upgrade can land in ~/.local/bin when the old
// directory is not writable.
func refreshRaptorSkills(out io.Writer, asJSON bool) ([]skillInstallationLite, error) {
	bin, err := ensureRaptorBinary()
	if err == nil {
		var skills []skillInstallationLite
		skills, err = installRaptorSkills(bin.Path, detectHarnesses())
		if !asJSON {
			reportRaptorSkills(out, skills, err)
		}
		return skills, err
	}
	if !asJSON {
		reportRaptorSkills(out, nil, err)
	}
	return nil, err
}

// raptorSkillAt reports whether host h reads a raptor skill: in its own skill
// folder, or in the user-level folder raptor writes for its agent.
func raptorSkillAt(h harness.Harness) bool {
	if _, err := os.Stat(filepath.Join(h.SkillDir, "raptor", "SKILL.md")); err == nil {
		return true
	}
	agent, ok := raptorAgents[h.Name]
	if !ok {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, "."+agent, "skills", "raptor", "SKILL.md"))
	return err == nil
}

// raptorSkillEverywhere reports whether every detected host raptor supports
// reads a raptor skill. Hosts raptor does not support (Antigravity) do not
// count; with no supported host at all it is false.
func raptorSkillEverywhere(hosts []harness.Harness) bool {
	n := 0
	for _, h := range hosts {
		if _, ok := raptorAgents[h.Name]; !ok {
			continue
		}
		if !raptorSkillAt(h) {
			return false
		}
		n++
	}
	return n > 0
}

// replacementAt reports whether host h has the skill that stands for
// capability c, for skillinstall.RetireReplacedGlobals.
func replacementAt(h harness.Harness, c string) bool {
	switch c {
	case "praxis-v1":
		_, err := os.Stat(filepath.Join(h.SkillDir, "praxis", "SKILL.md"))
		return err == nil
	case "raptor-v1":
		return raptorSkillAt(h)
	}
	return false
}
