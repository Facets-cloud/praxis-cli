package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/clifeed"
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

// raptorAgents maps a praxis host to the raptor --agent name that writes the
// same skills folder: ~/.claude/skills, ~/.agents/skills (Codex and Gemini
// CLI) and ~/.gemini/config/skills (Antigravity).
var raptorAgents = map[string]string{"claude-code": "claude", "codex": "agents", "gemini-cli": "agents", "antigravity": "antigravity"}

// raptorHostLayoutMarker is in `raptor install skill --help` from the first
// raptor that writes the folders above. Older raptors wrote ~/.codex/skills and
// ~/.gemini/skills, which Codex and Antigravity do not read.
const raptorHostLayoutMarker = "~/.gemini/config/skills"

// raptorSkillResult is one folder of `raptor install skill -o json`.
type raptorSkillResult struct {
	Host  string `json:"host"`
	Dir   string `json:"dir"`
	Error string `json:"error"`
}

// runInstallRaptorSkills asks raptor to install its own skill, at user level,
// for every host in one call. raptor records each folder and refreshes it
// after every upgrade, so praxis never owns a copy that can go stale. raptor
// writes the same folder that praxis reads for each host (harness.SkillDir).
// The skill does not depend on the praxis profile, so a project-scoped login
// installs it at user level too: a project copy would only give the host a
// second one. hosts must be the user-level hosts.
func runInstallRaptorSkills(raptor string, hosts []harness.Harness) ([]skillInstallationLite, error) {
	if raptor == "" {
		return nil, errors.New("raptor CLI is missing, so its skill was not installed")
	}
	base, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	args := []string{"install", "skill", "-o", "json"}
	done := map[string]bool{}
	for _, h := range hosts {
		if agent, ok := raptorAgents[h.Name]; ok && !done[agent] {
			done[agent] = true
			args = append(args, "--agent", agent)
		}
	}
	if len(done) == 0 {
		return nil, nil
	}
	if err := checkRaptorHostLayout(raptor); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, raptor, args...)
	cmd.Dir = base
	cmd.Env = append(os.Environ(), "RAPTOR_NO_UPDATE_CHECK=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()
	var report struct {
		Results []raptorSkillResult `json:"results"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		return nil, fmt.Errorf("raptor skill: %w: %s", errors.Join(runErr, err), strings.TrimSpace(stderr.String()))
	}
	return raptorSkillInstalls(hosts, report.Results, runErr)
}

// raptorSkillInstalls lists the hosts whose folder raptor wrote, and an error
// for each folder that failed.
func raptorSkillInstalls(hosts []harness.Harness, results []raptorSkillResult, runErr error) ([]skillInstallationLite, error) {
	failed := map[string]string{}
	var errs []error
	for _, r := range results {
		if r.Error != "" {
			failed[filepath.Clean(r.Dir)] = r.Error
			errs = append(errs, fmt.Errorf("raptor skill for %s: %s", r.Host, r.Error))
		}
	}
	if runErr != nil && len(errs) == 0 {
		errs = append(errs, fmt.Errorf("raptor skill: %w", runErr))
	}
	var installed []skillInstallationLite
	for _, h := range hosts {
		if _, ok := raptorAgents[h.Name]; !ok {
			continue
		}
		if _, bad := failed[filepath.Clean(h.SkillDir)]; bad || !raptorSkillAt(h) {
			continue
		}
		installed = append(installed, skillInstallationLite{Harness: h.Name, Path: filepath.Join(h.SkillDir, "raptor", "SKILL.md")})
	}
	return installed, errors.Join(errs...)
}

// checkRaptorHostLayout fails when the raptor at path predates the shared
// host layout, so praxis never asks it to write folders the hosts do not read.
func checkRaptorHostLayout(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "install", "skill", "--help").Output()
	if err != nil {
		return fmt.Errorf("raptor at %s did not answer `install skill --help`, so its skill was not installed: %w", path, err)
	}
	if !strings.Contains(string(out), raptorHostLayoutMarker) {
		return fmt.Errorf("raptor at %s is too old to install its skill for every agent host; run `praxis update`", path)
	}
	return nil
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
		clifeed.RecordRaptorSkillError(err)
		return skills, err
	}
	if !asJSON {
		reportRaptorSkills(out, nil, err)
	}
	clifeed.RecordRaptorSkillError(err)
	return nil, err
}

// raptorSkillAt reports whether host h reads a raptor skill in its own skill
// folder, which is where raptor writes it.
func raptorSkillAt(h harness.Harness) bool {
	_, err := os.Stat(filepath.Join(h.SkillDir, "raptor", "SKILL.md"))
	return err == nil
}

// raptorSkillEverywhere reports whether every detected host raptor supports
// reads a raptor skill. With no supported host at all it is false.
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
