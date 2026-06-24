package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/selfupdate"
)

// updateCheckInterval throttles how often the background check hits GitHub.
const updateCheckInterval = 24 * time.Hour

// updateCheckMaxWait caps how long Execute waits for the background check before
// giving up on the notice for this run. The select returns the instant the
// result is ready, so the warm-cache path (the overwhelming majority of runs)
// never waits — this bound only bites on the once-per-interval cold fetch, and
// is deliberately short so even that is barely perceptible. A short wait (vs
// pure non-blocking) is what lets the notice + cache write actually land for
// fast local commands, whose work finishes long before a network fetch could.
const updateCheckMaxWait = 3 * time.Second

// updateCheckRetryDelay pauses between the two live-fetch attempts. A var (not
// a const) so tests can zero it out and not sleep.
var updateCheckRetryDelay = 2 * time.Second

// updateCheckCache is the on-disk throttle state at paths.UpdateCheckCache().
type updateCheckCache struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version"`
}

// checkForUpdate returns the latest release tag when a newer version is
// available, otherwise an empty string. It is best-effort: any error (network,
// disk, API) yields "" so the calling command is never disturbed. A 24h file
// cache avoids hammering GitHub; on a cold/stale cache it performs one live
// fetch with one silent retry.
//
// The live fetch reuses cmd/update.go's fetchLatestRelease seam
// (= selfupdate.LatestRelease), so tests stub both flows the same way.
func checkForUpdate() string {
	if os.Getenv("PRAXIS_NO_UPDATE_CHECK") != "" {
		return ""
	}

	// Skip development builds. Released binaries are stamped with a clean
	// semver by goreleaser (e.g. "1.0.0"); local `make build` uses
	// `git describe --tags --always --dirty`, which yields "dev" (no tag),
	// a "-dirty" suffix on a modified tree, or a "-<n>-g<sha>" suffix when
	// ahead of the last tag. Nagging those would be noise.
	if isDevBuild(version) {
		return ""
	}
	currentClean := strings.TrimPrefix(version, "v")

	// Serve from cache while fresh.
	if cached, err := readUpdateCache(); err == nil && time.Since(cached.CheckedAt) < updateCheckInterval {
		return newerThan(currentClean, cached.LatestVersion)
	}

	// Live fetch with one silent retry on any error.
	rel := fetchLatestReleaseWithRetry()
	if rel == nil {
		// Record the attempt (empty LatestVersion) so an offline/API outage
		// honors the 24h throttle instead of re-fetching on every invocation.
		// The fresh-cache branch above treats an empty LatestVersion as "no
		// update" via newerThan, so this stays silent until the cache expires.
		_ = saveUpdateCache(updateCheckCache{CheckedAt: time.Now()})
		return "" // best-effort — stay silent
	}

	_ = saveUpdateCache(updateCheckCache{
		CheckedAt:     time.Now(),
		LatestVersion: rel.TagName,
	})

	return newerThan(currentClean, rel.TagName)
}

// gitDescribeAhead matches the "-<n>-g<sha>" suffix git describe appends when
// the build is ahead of the most recent tag (e.g. "v1.0.0-3-gabc1234").
var gitDescribeAhead = regexp.MustCompile(`-\d+-g[0-9a-f]+`)

// isDevBuild reports whether version looks like a local/development build
// rather than a clean published release, so the nag stays silent for it.
func isDevBuild(v string) bool {
	if v == "" || v == "dev" || strings.Contains(v, "dev") {
		return true
	}
	if strings.HasSuffix(v, "-dirty") {
		return true
	}
	return gitDescribeAhead.MatchString(v)
}

// newerThan reports latestTag (e.g. "v1.2.3") as the result when it differs
// from the current version, else "". Comparison is string equality after
// stripping the "v" prefix — matching praxis update (cmd/update.go) and raptor.
func newerThan(currentClean, latestTag string) string {
	latestClean := strings.TrimPrefix(latestTag, "v")
	if latestClean != "" && latestClean != currentClean {
		return latestTag
	}
	return ""
}

// fetchLatestReleaseWithRetry calls the fetchLatestRelease seam up to twice,
// pausing briefly between attempts. Returns nil if both attempts fail.
func fetchLatestReleaseWithRetry() *selfupdate.Release {
	for attempt := 1; attempt <= 2; attempt++ {
		rel, err := fetchLatestRelease()
		if err == nil && rel != nil {
			return rel
		}
		if attempt < 2 {
			time.Sleep(updateCheckRetryDelay)
		}
	}
	return nil
}

// readUpdateCache reads the throttle cache from disk.
func readUpdateCache() (updateCheckCache, error) {
	path, err := paths.UpdateCheckCache()
	if err != nil {
		return updateCheckCache{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return updateCheckCache{}, err
	}
	var c updateCheckCache
	return c, json.Unmarshal(data, &c)
}

// saveUpdateCache persists the throttle cache, creating ~/.praxis if needed.
func saveUpdateCache(c updateCheckCache) error {
	dir, err := paths.Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path, err := paths.UpdateCheckCache()
	if err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// printUpdateNotification writes the "update available" box to w (os.Stderr in
// production). Kept on stderr so it never pollutes a command's parseable
// stdout when an AI host spawns praxis as a subprocess.
func printUpdateNotification(latestVersion string, w io.Writer) {
	line1 := fmt.Sprintf("  ⚡ Update available: %s → %s", version, latestVersion)
	line2 := "  Run `praxis update` to install the latest version"
	line3 := "  Set PRAXIS_NO_UPDATE_CHECK=1 to silence this notice"

	// Use display-column width, not byte length, so wide characters (⚡, →)
	// don't throw off the right border alignment.
	width := maxInt(displayWidth(line1), displayWidth(line2), displayWidth(line3)) + 2
	border := strings.Repeat("─", width)

	fmt.Fprintln(w)
	fmt.Fprintf(w, "╭%s╮\n", border)
	fmt.Fprintf(w, "│%s│\n", padToWidth(line1, width))
	fmt.Fprintf(w, "│%s│\n", padToWidth("", width))
	fmt.Fprintf(w, "│%s│\n", padToWidth(line2, width))
	fmt.Fprintf(w, "│%s│\n", padToWidth(line3, width))
	fmt.Fprintf(w, "╰%s╯\n", border)
	fmt.Fprintln(w)
}

// skipUpdateCheck reports whether the background nag should be suppressed for
// this invocation, based on the raw args. Suppressed for version/update (which
// already report version state) and completion (its stdout is sourced by the
// shell — stray stderr would still surprise users debugging completion).
func skipUpdateCheck(args []string) bool {
	for _, a := range args {
		switch a {
		case "update", "version", "--version", "-v", "completion":
			return true
		}
	}
	return false
}

// displayWidth returns the number of terminal columns a string occupies. Wide
// characters (emoji, CJK, etc.) count as 2 columns; all others as 1.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// padToWidth appends spaces so that displayWidth(result) == width.
func padToWidth(s string, width int) string {
	pad := width - displayWidth(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

// wideRunes covers characters that occupy 2 terminal columns (East Asian
// Wide/Fullwidth, CJK, Hangul, emoji, misc symbols). unicode.Is binary-searches
// the sorted ranges — O(log n).
var wideRunes = &unicode.RangeTable{
	R16: []unicode.Range16{
		{0x1100, 0x115F, 1}, // Hangul Jamo
		{0x2329, 0x232A, 1}, // CJK angle brackets
		{0x2600, 0x27BF, 1}, // Misc Symbols (⚡ U+26A1) + Dingbats
		{0x2B00, 0x2BFF, 1}, // Misc Symbols and Arrows
		{0x2E80, 0x303E, 1}, // CJK Radicals, Kangxi, Bopomofo, etc.
		{0x3040, 0x33FF, 1}, // Hiragana, Katakana, Bopomofo, CJK Compat Jamo
		{0x3400, 0x4DBF, 1}, // CJK Unified Ideographs Extension A
		{0x4E00, 0x9FFF, 1}, // CJK Unified Ideographs
		{0xA000, 0xA4CF, 1}, // Yi Syllables
		{0xA960, 0xA97F, 1}, // Hangul Jamo Extended-A
		{0xAC00, 0xD7FF, 1}, // Hangul Syllables + Jamo Extended-B
		{0xF900, 0xFAFF, 1}, // CJK Compatibility Ideographs
		{0xFE10, 0xFE6F, 1}, // CJK Compatibility Forms, Small Form Variants
		{0xFF01, 0xFF60, 1}, // Fullwidth Latin + Halfwidth Katakana
		{0xFFE0, 0xFFE6, 1}, // Fullwidth currency/signs
	},
	R32: []unicode.Range32{
		{0x1F000, 0x1FAFF, 1}, // Emoji
		{0x20000, 0x2FFFD, 1}, // CJK Extension B–F + Compatibility Supplement
		{0x30000, 0x3FFFD, 1}, // CJK Extension G and beyond
	},
}

// runeWidth returns 2 for wide/fullwidth Unicode characters, 1 otherwise.
func runeWidth(r rune) int {
	if unicode.Is(wideRunes, r) {
		return 2
	}
	return 1
}

func maxInt(vals ...int) int {
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
}
