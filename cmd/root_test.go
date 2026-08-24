package cmd

import (
	"bytes"
	"sort"
	"strings"
	"testing"
)

func TestRoot_HelpListsAllShippedCommands(t *testing.T) {
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs([]string{"--help"})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute --help err = %v", err)
	}
	cmds := availableCommands(buf.String())
	if len(cmds) == 0 {
		t.Fatalf("parsed no commands from --help; output was:\n%s", buf.String())
	}
	// Every shipped top-level command must be discoverable from `praxis --help`.
	for _, want := range []string{
		"agents", "completion", "duty", "git-credential", "ig", "list-skills",
		"login", "logout", "mcp", "memory", "profiles", "refresh-skills",
		"status", "update", "version",
	} {
		if !cmds[want] {
			t.Errorf("--help doesn't list %q\ncommands found: %v", want, sortedCommandNames(cmds))
		}
	}
	// Commands removed in the major-version cleanup (formerly deprecated:
	// init, install-skill, uninstall-skill, whoami, use) and never-shipped
	// stubs must NOT appear in --help. `use` survives only as a SUBcommand of
	// profiles, which is a different command path and not listed here.
	for _, mustNot := range []string{
		"init", "install-skill", "uninstall-skill",
		"whoami", "use",
		// Stubs from earlier releases that haven't been reimplemented
		"doctor", "configure",
	} {
		if cmds[mustNot] {
			t.Errorf("--help still advertises removed command %q", mustNot)
		}
	}
}

// availableCommands returns the command names cobra lists under "Available
// Commands:".
//
// It parses that section instead of substring-scanning the whole help page,
// because a bare strings.Contains(help, "use") also matches the English word
// "use" in any flag description — which made this test fail the moment the
// global --profile flag described itself as "profile to use". The assertions
// are about COMMANDS, so the parsing has to be too.
func availableCommands(help string) map[string]bool {
	names := map[string]bool{}
	inSection := false
	for _, line := range strings.Split(help, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Available Commands:") {
			inSection = true
			continue
		}
		if !inSection {
			continue
		}
		if trimmed == "" {
			break // a blank line ends the section (Flags: comes next)
		}
		if fields := strings.Fields(trimmed); len(fields) > 0 {
			names[fields[0]] = true
		}
	}
	return names
}

func sortedCommandNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func TestAvailableCommands_ParsesOnlyTheCommandSection(t *testing.T) {
	help := "Usage:\n  praxis [command]\n\n" +
		"Available Commands:\n" +
		"  login       Authenticate\n" +
		"  profiles    List all profiles\n" +
		"\n" +
		"Flags:\n" +
		"  -p, --profile string   credentials profile to use for this invocation\n" +
		"      --whoami           a flag that merely mentions a removed command\n"

	got := availableCommands(help)
	if !got["login"] || !got["profiles"] {
		t.Errorf("commands = %v, want login and profiles", sortedCommandNames(got))
	}
	// The words below appear in the flags section only — the exact false
	// positive a whole-page substring scan produces.
	for _, notACommand := range []string{"use", "whoami", "-p,", "--profile", "Flags:"} {
		if got[notACommand] {
			t.Errorf("%q parsed as a command; flag descriptions must not leak in", notACommand)
		}
	}
}

// We don't test the global --version flag directly: cobra resolves it
// before our Run handlers and its output formatting is not stable across
// state shared with other tests in this package. The `version` SUBCOMMAND
// (TestVersionCmd_PrintsAllFields in version_test.go) gives us the same
// signal with a clean test boundary.
