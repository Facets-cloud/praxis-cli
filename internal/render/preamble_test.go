package render

import (
	"strings"
	"testing"
)

func TestExecutionPreambleShape(t *testing.T) {
	p := ExecutionPreamble
	if !strings.HasPrefix(p, "> **Execution context**") {
		// Cap the preview at len(p) — a regression that empties or
		// truncates the constant would otherwise panic here and bury
		// the real failure message.
		previewLen := min(len(p), 80)
		t.Fatalf("preamble should begin with the execution-context blockquote, got:\n%s", p[:previewLen])
	}
	if !strings.Contains(p, "praxis mcp") {
		t.Fatal("preamble should teach the praxis mcp rewrite rule")
	}
	if !strings.Contains(p, "~/.praxis/mcp-tools.json") {
		t.Fatal("preamble should reference the manifest snapshot path")
	}
	if !strings.HasSuffix(p, "\n") {
		t.Fatal("preamble should end with a trailing newline so callers can concatenate cleanly")
	}
}
