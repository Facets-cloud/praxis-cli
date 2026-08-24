package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/agent"
)

// execAgent drives `praxis agent …` through the ROOT command, the way a user
// does. Invoking Execute on a registered subcommand re-runs root with root's own
// args (the `go test` flags in a test binary) and ignores SetArgs on the child.
func execAgent(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(append([]string{"agent"}, args...))
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	err := rootCmd.Execute()
	return buf.String(), err
}

// captureRuntime replaces both runtime seams and reports what each received, so
// these tests assert the argv handed to the agent without launching a model,
// spawning MCP children, or taking over stdio.
func captureRuntime(t *testing.T) (native *[]string, skills *[]string, calls *int) {
	t.Helper()
	prevNative, prevSkills := agentRunNative, agentRunSkills
	var gotNative, gotSkills []string
	var n int
	agentRunNative = func(_ context.Context, args []string) int {
		gotNative, n = args, n+1
		return 0
	}
	agentRunSkills = func(_ context.Context, args []string) int {
		gotSkills, n = args, n+1
		return 0
	}
	t.Cleanup(func() { agentRunNative, agentRunSkills = prevNative, prevSkills })
	return &gotNative, &gotSkills, &n
}

func equalArgs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// Each subcommand exists to reach one capability of the agent runtime, and the
// runtime selects that capability from argv itself: a leading subcommand word for
// plugin/mcp/slack/agents, a flag for the ACP and SDK servers. Getting the prefix
// wrong silently starts a chat session instead, so the prefix — and the fact that
// user arguments ride behind it untouched — is the contract.
func TestAgentCmd_ForwardsRuntimeSelectorAndArgs(t *testing.T) {
	t.Setenv("PRAXIS_EXPERIMENTAL", "1")

	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"plugin", []string{"plugin", "install", "reviewer@market", "--scope", "project"},
			[]string{"plugin", "install", "reviewer@market", "--scope", "project"}},
		{"mcp", []string{"mcp", "add", "serena", "uvx", "serena-mcp"},
			[]string{"mcp", "add", "serena", "uvx", "serena-mcp"}},
		{"slack", []string{"slack", "status"}, []string{"slack", "status"}},
		{"sessions", []string{"sessions", "-prune-empty", "-older-than", "48h"},
			[]string{"agents", "-prune-empty", "-older-than", "48h"}},
		{"acp", []string{"acp"}, []string{"-acp"}},
		{"sdk", []string{"sdk"}, []string{"-sdk"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotNative, _, calls := captureRuntime(t)
			if _, err := execAgent(t, tc.args...); err != nil {
				t.Fatalf("praxis agent %v err = %v", tc.args, err)
			}
			if *calls != 1 {
				t.Fatalf("runtime called %d times, want 1", *calls)
			}
			if !equalArgs(*gotNative, tc.want) {
				t.Fatalf("forwarded %v, want %v", *gotNative, tc.want)
			}
		})
	}
}

// The skill report is a different runtime entry point on purpose: it builds a
// client with MCP, LSP and session persistence off, so reading local analytics
// cannot spawn stdio children. Routing it through the native runner instead would
// re-enable all of that.
func TestAgentCmd_SkillsUsesReportEntryPoint(t *testing.T) {
	t.Setenv("PRAXIS_EXPERIMENTAL", "1")
	gotNative, gotSkills, calls := captureRuntime(t)

	if _, err := execAgent(t, "skills", "-inactive", "168h", "-json"); err != nil {
		t.Fatalf("praxis agent skills err = %v", err)
	}
	if *calls != 1 {
		t.Fatalf("runtime called %d times, want 1", *calls)
	}
	if len(*gotNative) != 0 {
		t.Fatalf("skills went through the native runner: %v", *gotNative)
	}
	if want := []string{"-inactive", "168h", "-json"}; !equalArgs(*gotSkills, want) {
		t.Fatalf("skills args = %v, want %v", *gotSkills, want)
	}
}

// Flag parsing is disabled on these commands so runtime flags survive intact,
// which means --experimental has to be recognized by hand. Forwarding it would
// reach the runtime as an unknown flag and abort the command it was enabling.
func TestAgentCmd_ExperimentalEnablesAndIsNotForwarded(t *testing.T) {
	t.Setenv("PRAXIS_EXPERIMENTAL", "")
	gotNative, _, calls := captureRuntime(t)

	if _, err := execAgent(t, "plugin", "--experimental", "list"); err != nil {
		t.Fatalf("praxis agent plugin --experimental list err = %v", err)
	}
	if *calls != 1 {
		t.Fatalf("runtime called %d times, want 1", *calls)
	}
	if want := []string{"plugin", "list"}; !equalArgs(*gotNative, want) {
		t.Fatalf("forwarded %v, want %v", *gotNative, want)
	}
}

func TestAgentCmd_GatedWithoutExperimental(t *testing.T) {
	t.Setenv("PRAXIS_EXPERIMENTAL", "")
	_, _, calls := captureRuntime(t)

	_, err := execAgent(t, "plugin", "list")
	if err == nil {
		t.Fatal("praxis agent plugin list was accepted with the experimental gate off")
	}
	if !errors.Is(err, agent.ErrExperimentalDisabled) {
		t.Fatalf("err = %v, want ErrExperimentalDisabled", err)
	}
	if *calls != 0 {
		t.Fatalf("runtime called %d times, want 0 when gated", *calls)
	}
}

// --help must print this CLI's help rather than reaching the runtime: with flag
// parsing disabled cobra never intercepts it, and the runtime would answer with
// usage text naming a binary the user does not have.
func TestAgentCmd_HelpDoesNotReachRuntime(t *testing.T) {
	t.Setenv("PRAXIS_EXPERIMENTAL", "1")
	_, _, calls := captureRuntime(t)

	out, err := execAgent(t, "plugin", "--help")
	if err != nil {
		t.Fatalf("praxis agent plugin --help err = %v", err)
	}
	if *calls != 0 {
		t.Fatalf("runtime called %d times for --help, want 0", *calls)
	}
	if !strings.Contains(out, "marketplace add") {
		t.Fatalf("plugin help missing the runtime's commands:\n%s", out)
	}
}

// "praxis agent" (this runtime) and "praxis agents" (installed agent files) are
// one letter apart and mean different things. The parent help is the only place a
// user can tell them apart, so it has to say so and list what it covers.
func TestAgentCmd_ParentHelpDistinguishesAgentsCommand(t *testing.T) {
	out, err := execAgent(t, "--help")
	if err != nil {
		t.Fatalf("praxis agent --help err = %v", err)
	}
	for _, want := range []string{
		`"praxis agents" (plural)`,
		"plugin",
		"mcp",
		"slack",
		"sessions",
		"skills",
		"acp",
		"sdk",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("praxis agent --help missing %q\nfull output:\n%s", want, out)
		}
	}
}
