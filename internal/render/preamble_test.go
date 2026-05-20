package render

import (
	"strings"
	"testing"
)

func TestExecutionPreambleShape(t *testing.T) {
	p := ExecutionPreamble
	if !strings.HasPrefix(p, "> **Execution context**") {
		t.Fatalf("preamble should begin with the execution-context blockquote, got:\n%s", p[:80])
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
