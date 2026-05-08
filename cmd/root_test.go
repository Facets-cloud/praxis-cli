package cmd

import (
	"bytes"
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
	out := buf.String()
	// User-facing surface: 9 commands.
	for _, want := range []string{
		"login", "logout", "status", "mcp", "refresh-skills",
		"update", "version", "completion",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--help output missing %q\nfull output:\n%s", want, out)
		}
	}
	// Hidden commands must NOT appear in --help — they still work when
	// invoked directly (with a deprecation warning) but the visible
	// surface must stay small.
	for _, mustNot := range []string{
		"init", "install-skill", "uninstall-skill",
		"list-skills", "whoami", "use",
		// Stubs from earlier releases that haven't been reimplemented
		"doctor", "configure",
	} {
		if strings.Contains(out, mustNot) {
			t.Errorf("--help still advertises hidden/removed command %q", mustNot)
		}
	}
}

// We don't test the global --version flag directly: cobra resolves it
// before our Run handlers and its output formatting is not stable across
// state shared with other tests in this package. The `version` SUBCOMMAND
// (TestVersionCmd_PrintsAllFields in version_test.go) gives us the same
// signal with a clean test boundary.
