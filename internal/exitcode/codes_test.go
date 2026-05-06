package exitcode

import "testing"

// Codes are part of the public contract — once published, they cannot
// change without breaking AI hosts that depend on the meanings. This
// test pins the numbers so a future refactor can't silently renumber.
func TestPublishedCodesArePinned(t *testing.T) {
	cases := map[string]int{
		"OK":       OK,
		"Error":    Error,
		"Usage":    Usage,
		"Auth":     Auth,
		"NoConfig": NoConfig,
		"Network":  Network,
		"NoHost":   NoHost,
	}
	want := map[string]int{
		"OK":       0,
		"Error":    1,
		"Usage":    2,
		"Auth":     3,
		"NoConfig": 4,
		"Network":  5,
		"NoHost":   6,
	}
	for name, got := range cases {
		if got != want[name] {
			t.Errorf("exitcode.%s = %d, want %d (PUBLISHED CONTRACT — do not change)", name, got, want[name])
		}
	}
}
