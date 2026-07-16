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
	// raptor is a local CLI, not a gateway tool — the preamble must say so
	// (and never route raptor through `praxis mcp`).
	if !strings.Contains(p, "raptor") || !strings.Contains(p, "raptor login") {
		t.Fatal("preamble should explain raptor is a local CLI run directly (with install/login fallback)")
	}
	// Freshness: offer `raptor upgrade` when status reports raptor stale, and
	// describe `tools` correctly as an ARRAY (not an object path raptor.stale).
	if !strings.Contains(p, "raptor upgrade") {
		t.Fatal("preamble should offer `raptor upgrade` when raptor is stale")
	}
	if strings.Contains(p, "raptor.stale") {
		t.Fatal("preamble uses the wrong shape `raptor.stale`; `tools` is an array — find the raptor entry")
	}
	if !strings.HasSuffix(p, "\n") {
		t.Fatal("preamble should end with a trailing newline so callers can concatenate cleanly")
	}
}
