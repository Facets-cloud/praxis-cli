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
	for _, want := range []string{"completion", "logout", "update", "version", "install-skill", "uninstall-skill", "list-skills"} {
		if !strings.Contains(out, want) {
			t.Errorf("--help output missing %q\nfull output:\n%s", want, out)
		}
	}
	// Removed/never-implemented commands should NOT appear in help.
	for _, mustNot := range []string{"login", "whoami", "praxis mcp", "praxis doctor"} {
		if strings.Contains(out, mustNot) {
			t.Errorf("--help still advertises removed command %q", mustNot)
		}
	}
}

// We don't test the global --version flag directly: cobra resolves it
// before our Run handlers and its output formatting is not stable across
// state shared with other tests in this package. The `version` SUBCOMMAND
// (TestVersionCmd_PrintsAllFields in version_test.go) gives us the same
// signal with a clean test boundary.
