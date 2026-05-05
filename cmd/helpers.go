package cmd

import (
	"fmt"
	"os"
)

// notImplemented prints a friendly stub message and exits 2. Used for
// commands that exist as cobra entries but whose implementation lands in
// a later build phase. Phase 2 (skill install machinery) and Phase 3
// (server-touching commands) will replace these calls.
func notImplemented(phase int, what string) {
	fmt.Fprintf(os.Stderr,
		"praxis: %s is not yet implemented (lands in Phase %d).\n",
		what, phase)
	os.Exit(2)
}
