package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/raptorinstall"
	"github.com/Facets-cloud/praxis-cli/internal/render"
)

var ensureRaptorBinary = raptorinstall.Ensure
var updateRaptor = runRaptorUpgrade

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
