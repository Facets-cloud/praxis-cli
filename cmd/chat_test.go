package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// execChat drives the command the way a user does: through the ROOT command.
// chatCmd.Execute() would not run chat at all — cobra's ExecuteC starts with
// `if c.HasParent() { return c.Root().ExecuteC() }`, so invoking Execute on a
// registered subcommand re-runs root with root's args (in a test binary, the
// `go test` flags) and silently ignores SetArgs on the child.
func execChat(t *testing.T, args ...string) (string, error) {
	t.Helper()
	// Reset BEFORE the run, not in a t.Cleanup: cleanups registered by a helper run
	// when the test ends, so a table-driven caller would otherwise inherit the flag
	// marks of the previous case and assert against the wrong conflict.
	resetChatFlagState()
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(append([]string{"chat"}, args...))
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		resetChatFlagState()
	})
	err := rootCmd.Execute()
	return buf.String(), err
}

// resetChatFlagState restores each flag's default VALUE and clears the "was this
// flag set" mark cobra records on the shared command. Both matter: flag-group
// enforcement reads the marks, so a stale one manufactures a conflict nobody asked
// for — and a leftover help=true makes cobra's execute() return flag.ErrHelp before
// it ever validates, which ExecuteC turns into a nil error that looks like success.
func resetChatFlagState() {
	chatAgents, chatPrompt, chatResume, chatSessionID = false, "", "", ""
	for _, name := range []string{"agents", "prompt", "resume", "session-id", "help"} {
		if flag := chatCmd.Flags().Lookup(name); flag != nil {
			_ = flag.Value.Set(flag.DefValue)
			flag.Changed = false
		}
	}
}

// `praxis agents` is this CLI's installed-agent-file lister, and the skills praxis
// installs into AI hosts call it with --json. The harness session dashboard is
// therefore a START VIEW of the agent command, `praxis chat --agents`, and the help
// has to say so — otherwise the two meanings of "agents" are indistinguishable to
// a user who just wants the dashboard.
func TestChatCmd_AgentsFlagDocumentsDashboardStartView(t *testing.T) {
	out, err := execChat(t, "--help")
	if err != nil {
		t.Fatalf("chat --help err = %v", err)
	}
	for _, want := range []string{
		"--agents",
		"start on the session dashboard",
		"'praxis agents' is unrelated",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("chat --help missing %q\nfull output:\n%s", want, out)
		}
	}
}

// The dashboard chooses which session to open, and the harness clears the startup
// prompt, resume path, and session id for that row (tui.loadDashboardApplication).
// Silently dropping those flags would look like the agent ignoring the user, so the
// combination is refused before anything launches.
func TestChatCmd_AgentsRejectsSingleSessionIdentityFlags(t *testing.T) {
	t.Setenv("PRAXIS_EXPERIMENTAL", "1")
	for _, conflicting := range [][]string{
		{"--agents", "--prompt", "fix the bug"},
		{"--agents", "--resume", "/tmp/session.jsonl"},
		{"--agents", "--session-id", "abc123"},
	} {
		_, err := execChat(t, conflicting...)
		if err == nil {
			t.Fatalf("chat %v was accepted; want a conflicting-flag error", conflicting)
		}
		if !strings.Contains(err.Error(), "--agents cannot be combined with") {
			t.Fatalf("chat %v err = %v, want a conflicting-flag error", conflicting, err)
		}
	}
}

// Turning the dashboard OFF explicitly is a plain single-session run, and every
// identity flag is legal there. Checking "was --agents passed" instead of its
// value — which is what cobra's MarkFlagsMutuallyExclusive does — rejects
// `--agents=false --prompt …` for a conflict that does not exist.
func TestRejectDashboardIdentityFlags_ValueNotPresence(t *testing.T) {
	t.Cleanup(resetChatFlagState)

	resetChatFlagState()
	chatAgents, chatPrompt = false, "fix the bug"
	if err := rejectDashboardIdentityFlags(); err != nil {
		t.Fatalf("--agents=false with --prompt rejected: %v", err)
	}

	for _, tc := range []struct {
		name  string
		setup func()
	}{
		{"prompt", func() { chatAgents, chatPrompt = true, "fix the bug" }},
		{"resume", func() { chatAgents, chatResume = true, "/tmp/session.jsonl" }},
		{"session-id", func() { chatAgents, chatSessionID = true, "abc123" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetChatFlagState()
			tc.setup()
			if err := rejectDashboardIdentityFlags(); err == nil {
				t.Fatalf("--agents with --%s accepted; the dashboard would drop it", tc.name)
			}
		})
	}

	resetChatFlagState()
	chatAgents = true
	if err := rejectDashboardIdentityFlags(); err != nil {
		t.Fatalf("--agents alone rejected: %v", err)
	}
}
