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

	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/raptorinstall"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
)

var ensureRaptorBinary = raptorinstall.Ensure
var installRaptorSkills = runInstallRaptorSkills
var updateRaptor = runRaptorUpgrade

type raptorUpgradeResult struct {
	Attempted bool                 `json:"attempted"`
	Completed bool                 `json:"completed"`
	Binary    raptorinstall.Result `json:"binary"`
	Output    string               `json:"output,omitempty"`
	Warning   string               `json:"warning,omitempty"`
}

func runRaptorUpgrade(out io.Writer, asJSON, yes bool) (result raptorUpgradeResult, err error) {
	result.Binary, err = ensureRaptorBinary()
	if err != nil {
		result.Warning = err.Error()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	args := []string{"upgrade"}
	if yes {
		args = append(args, "--yes")
	}
	cmd := exec.CommandContext(ctx, result.Binary.Path, args...)
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "RAPTOR_NO_SKILLS_UPGRADE=") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "RAPTOR_NO_SKILLS_UPGRADE=1")
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
	result.Output = captured.String()
	result.Completed = err == nil
	if err != nil {
		err = fmt.Errorf("raptor upgrade failed: %w", err)
		result.Warning = err.Error()
	}
	return
}

func finishToolUpdate(out io.Writer, asJSON, yes bool, payload map[string]any) error {
	raptor, err := updateRaptor(out, asJSON, yes)
	if asJSON {
		payload["raptor"] = raptor
		return errors.Join(err, render.JSON(out, payload))
	}
	if raptor.Binary.PathWarning != "" {
		fmt.Fprintln(out, raptor.Binary.PathWarning)
	}
	return err
}

func runInstallRaptorSkills(hosts []harness.Harness) ([]skillinstall.Installation, error) {
	binary, _ := raptorinstall.Find()
	return skillinstall.InstallRaptor(binary, hosts)
}

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
