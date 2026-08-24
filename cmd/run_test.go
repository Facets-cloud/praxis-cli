package cmd

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/agent"
)

// execRun drives `praxis run …` through the ROOT command; see execChat for why
// invoking Execute on a registered subcommand does not work.
func execRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetRunFlagState()
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(append([]string{"run"}, args...))
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		resetRunFlagState()
	})
	err := rootCmd.Execute()
	return buf.String(), err
}

// resetRunFlagState clears the flag values bound into runOpts and the "was this
// set" marks cobra keeps on the shared command, so one case cannot inherit the
// previous one's prompt, sandbox choice, or a leftover help=true that makes
// cobra return before it ever runs.
func resetRunFlagState() {
	runOpts = agent.HeadlessArgs{}
	runExperimental = false
	for _, name := range []string{
		"experimental", "prompt", "prompt-file", "model", "thinking", "cwd",
		"max-turns", "sandbox", "sandbox-exec", "add-dir", "permission-rule",
		"result-json", "help",
	} {
		if flag := runCmd.Flags().Lookup(name); flag != nil {
			_ = flag.Value.Set(flag.DefValue)
			flag.Changed = false
		}
	}
}

func argIndex(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

// The modelled flags exist for discoverability, but the runtime ships flags on
// its own cadence. Everything after -- reaches it verbatim and LAST, so a
// passthrough flag overrides the modelled value the runtime parsed earlier
// rather than being shadowed by it.
func TestRunCmd_PassthroughAfterDashDash(t *testing.T) {
	t.Setenv("PRAXIS_EXPERIMENTAL", "1")
	gotNative, _, calls := captureRuntime(t)

	if _, err := execRun(t, "--prompt", "hi", "--", "-reflex-capture", "true"); err != nil {
		t.Fatalf("praxis run … -- … err = %v", err)
	}
	if *calls != 1 {
		t.Fatalf("runtime called %d times, want 1", *calls)
	}
	args := *gotNative
	if i := argIndex(args, "-reflex-capture"); i < 0 || i+1 >= len(args) || args[i+1] != "true" {
		t.Fatalf("passthrough flag missing or malformed: %v", args)
	}
	if argIndex(args, "-reflex-capture") < argIndex(args, "-prompt") {
		t.Fatalf("passthrough args must come last, got %v", args)
	}
}

// A bare positional means the user typed a prompt without --prompt. Forwarding
// it would run an empty prompt and forwarding it as a positional would make the
// runtime reject it far from the typo, so it is refused here by name.
func TestRunCmd_RejectsStrayPositional(t *testing.T) {
	t.Setenv("PRAXIS_EXPERIMENTAL", "1")
	_, _, calls := captureRuntime(t)

	_, err := execRun(t, "fix the bug")
	if err == nil {
		t.Fatal("praxis run \"fix the bug\" was accepted; want an argument error")
	}
	if !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("err = %v, want an unexpected-argument error", err)
	}
	if *calls != 0 {
		t.Fatalf("runtime called %d times, want 0", *calls)
	}
}

// The process sandbox re-execs a child that speaks the runtime's bare flag
// dialect. The runtime looks for a sibling or PATH prx / praxis-native and finds
// neither next to a binary named praxis, so praxis must point it at itself —
// otherwise --sandbox process fails with "requires a child executable".
func TestRunCmd_SandboxProcessDefaultsChildToSelf(t *testing.T) {
	t.Setenv("PRAXIS_EXPERIMENTAL", "1")
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() err = %v", err)
	}

	gotNative, _, _ := captureRuntime(t)
	if _, err := execRun(t, "--prompt", "hi", "--sandbox", "process"); err != nil {
		t.Fatalf("praxis run --sandbox process err = %v", err)
	}
	args := *gotNative
	i := argIndex(args, "-sandbox-exec")
	if i < 0 || i+1 >= len(args) {
		t.Fatalf("-sandbox-exec not passed for --sandbox process: %v", args)
	}
	if args[i+1] != self {
		t.Fatalf("-sandbox-exec = %q, want this binary %q", args[i+1], self)
	}

	// An explicit choice wins: the user may point at a real prx build.
	gotNative, _, _ = captureRuntime(t)
	if _, err := execRun(t, "--prompt", "hi", "--sandbox", "process", "--sandbox-exec", "/usr/local/bin/prx"); err != nil {
		t.Fatalf("praxis run --sandbox-exec err = %v", err)
	}
	args = *gotNative
	if i := argIndex(args, "-sandbox-exec"); i < 0 || args[i+1] != "/usr/local/bin/prx" {
		t.Fatalf("explicit --sandbox-exec was overridden: %v", args)
	}
}

// Other sandbox levels do not re-exec anything, so praxis must not inject a
// child path the runtime would then have to justify.
func TestRunCmd_NoSandboxExecWithoutProcessSandbox(t *testing.T) {
	t.Setenv("PRAXIS_EXPERIMENTAL", "1")
	gotNative, _, _ := captureRuntime(t)

	if _, err := execRun(t, "--prompt", "hi", "--sandbox", "workspace"); err != nil {
		t.Fatalf("praxis run --sandbox workspace err = %v", err)
	}
	if i := argIndex(*gotNative, "-sandbox-exec"); i >= 0 {
		t.Fatalf("-sandbox-exec injected for a non-process sandbox: %v", *gotNative)
	}
}

func TestRunCmd_GatedWithoutExperimental(t *testing.T) {
	t.Setenv("PRAXIS_EXPERIMENTAL", "")
	_, _, calls := captureRuntime(t)

	_, err := execRun(t, "--prompt", "hi")
	if err == nil {
		t.Fatal("praxis run was accepted with the experimental gate off")
	}
	if !errors.Is(err, agent.ErrExperimentalDisabled) {
		t.Fatalf("err = %v, want ErrExperimentalDisabled", err)
	}
	if *calls != 0 {
		t.Fatalf("runtime called %d times, want 0 when gated", *calls)
	}
}

// The flag surface is the point of this command: it is what a scripted caller
// discovers with --help instead of reading the runtime's source.
func TestRunCmd_HelpDocumentsPassthroughAndContainment(t *testing.T) {
	out, err := execRun(t, "--help")
	if err != nil {
		t.Fatalf("praxis run --help err = %v", err)
	}
	for _, want := range []string{
		"--sandbox",
		"--add-dir",
		"--permission-rule",
		"--output-schema",
		"--max-time",
		"passed to the agent runtime verbatim",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("praxis run --help missing %q\nfull output:\n%s", want, out)
		}
	}
}
