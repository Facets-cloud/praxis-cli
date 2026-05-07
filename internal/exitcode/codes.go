// Package exitcode names the process exit codes praxis returns. AI hosts
// shelling out to praxis read these to decide what to do next.
//
// Stable across releases — once a code's meaning is published, it does
// not change. New codes get new numbers.
package exitcode

const (
	OK       = 0 // success
	Error    = 1 // generic / unexpected failure
	Usage    = 2 // bad command-line arguments
	Auth     = 3 // missing / invalid / expired credentials → run `praxis login`
	NoConfig = 4 // no Praxis URL resolvable (rare — default is hardcoded)
	Network  = 5 // network unreachable / timed out
	NoHost   = 6 // no AI host detected (for `install-skill` etc.)
)
