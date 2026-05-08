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
	// v0.7 surface: 8 user-facing commands.
	for _, want := range []string{
		"login", "logout", "status", "mcp",
		"update", "version", "completion",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--help output missing %q\nfull output:\n%s", want, out)
		}
	}
	// Commands hidden (deprecated) in v0.7 must NOT appear in --help —
	// they still work when invoked directly (with a deprecation warning)
	// but the surface must visibly shrink.
	for _, mustNot := range []string{
		"init", "install-skill", "uninstall-skill", "refresh-skills",
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
