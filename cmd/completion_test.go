package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestCompletionCmd_AllShellsEmitNonEmpty(t *testing.T) {
	tests := []struct {
		shell   string
		wantSub string // a substring distinctive to that shell's output
	}{
		{"bash", "complete"},
		{"zsh", "compdef"},
		{"fish", "complete"},
		{"powershell", "Register-ArgumentCompleter"},
	}
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			var buf bytes.Buffer
			completionCmd.SetOut(&buf)
			if err := completionCmd.RunE(completionCmd, []string{tt.shell}); err != nil {
				t.Fatalf("RunE err = %v", err)
			}
			out := buf.String()
			if len(out) == 0 {
				t.Fatalf("%s completion produced no output", tt.shell)
			}
			if !strings.Contains(out, tt.wantSub) {
				t.Errorf("%s completion missing %q (got %d bytes)", tt.shell, tt.wantSub, len(out))
			}
		})
	}
}

func TestCompletionCmd_RejectsUnknownShell(t *testing.T) {
	// Subcommand args validation kicks in only via the root tree, so dispatch
	// through rootCmd. SetOut both the root and the subcommand so cobra's
	// usage-on-error message lands somewhere we can swallow.
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs([]string{"completion", "tcsh"})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error for unknown shell, got nil")
	}
}
