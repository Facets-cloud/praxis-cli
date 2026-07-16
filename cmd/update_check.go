package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Facets-cloud/praxis-cli/internal/paths"
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

// toolCacheEntry is the per-tool throttle state persisted at
// paths.UpdateCheckCache(). freshnessCache maps tool name → entry: ONE file for
// all tools, so praxis and raptor never fragment into separate caches.
type toolCacheEntry struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version"`
}
type freshnessCache map[string]toolCacheEntry

// toolSpec describes a tool whose freshness praxis tracks. praxis and raptor
// differ ONLY here — the cache, comparator, renderer, and throttle are all
// shared. UpgradeHint is the command the user runs to upgrade; praxis's
// `praxis update` self-replaces the binary, raptor's `raptor upgrade` is
// nudge-only (praxis never runs it).
type toolSpec struct {
	Name        string
	UpgradeHint string
	// current returns (localVersion, installed, checkable): praxis is always
	// installed and checkable unless it's a dev build; raptor is installed iff
	// `raptor` is on PATH, and checkable iff installed.
	current func() (version string, installed, checkable bool)
	// fetchTag returns the tool's latest published tag (a network call routed
	// through a package-var seam so tests stub it).
	fetchTag func() (string, error)
}

func praxisSpec() toolSpec {
	return toolSpec{
		Name: "praxis", UpgradeHint: "praxis update",
		current: func() (string, bool, bool) { return version, true, !isDevBuild(version) },
		fetchTag: func() (string, error) {
			r, err := fetchLatestRelease()
			if err != nil || r == nil {
				return "", err
			}
			return r.TagName, nil
		},
	}
}

func raptorSpec() toolSpec {
	return toolSpec{
		Name: "raptor", UpgradeHint: "raptor upgrade",
		current:  func() (string, bool, bool) { v, ok := raptorLocalVersion(); return v, ok, ok },
		fetchTag: fetchRaptorTag,
	}
}

// freshnessTools is the registry every surface (nag, status, login) iterates.
func freshnessTools() []toolSpec { return []toolSpec{praxisSpec(), raptorSpec()} }

// Freshness is one tool's freshness result, surfaced to status/login/nag.
type Freshness struct {
	Tool      string `json:"tool"`
	Installed bool   `json:"installed"`
	Current   string `json:"current,omitempty"`
	Latest    string `json:"latest,omitempty"`
	Stale     bool   `json:"stale"`
}

// checkTool resolves one tool's freshness: best-effort, 24h-cached, never fatal.
// live forces a network re-check (e.g. `status --refresh`); otherwise a fresh
// cache entry is served without a fetch. The kill-switch and non-checkable
// tools (dev build / raptor absent) short-circuit to "not stale".
func checkTool(spec toolSpec, now time.Time, live bool) Freshness {
	cur, installed, checkable := spec.current()
	f := Freshness{Tool: spec.Name, Installed: installed, Current: cur}
	if os.Getenv("PRAXIS_NO_UPDATE_CHECK") != "" || !checkable {
		return f
	}
	f.Latest = latestTagFor(spec, now, live)
	f.Stale = f.Latest != "" && compareSemver(cur, f.Latest) < 0
	return f
}

// checkForUpdate preserves praxis's original nag entry point: the latest tag
// when praxis itself is behind, else "". Now a thin call over the shared engine.
func checkForUpdate() string {
	if f := checkTool(praxisSpec(), time.Now(), false); f.Stale {
		return f.Latest
	}
	return ""
}

// latestTagFor returns a tool's latest tag, served from the 24h cache unless
// live. A failed fetch caches an empty tag so an offline/API outage honors the
// throttle (compareSemver treats "" as not-stale) instead of re-fetching every run.
func latestTagFor(spec toolSpec, now time.Time, live bool) string {
	if !live {
		if c, err := readFreshnessCache(); err == nil {
			if e, ok := c[spec.Name]; ok && now.Sub(e.CheckedAt) < updateCheckInterval {
				return e.LatestVersion
			}
		}
	}
	tag := fetchTagWithRetry(spec.fetchTag)
	putCacheEntry(spec.Name, toolCacheEntry{CheckedAt: now, LatestVersion: tag})
	return tag
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

// newerThan reports latestTag (e.g. "v1.2.3") as the result only when it is
// strictly semver-newer than current, else "". A plain string-inequality test
// is wrong here: the throttle cache can hold a tag that was latest when written
// but is now OLDER than the installed binary (e.g. the user released vX and
// self-upgraded within the 24h window), which would otherwise nag backward.
func newerThan(current, latestTag string) string {
	if latestTag == "" {
		return ""
	}
	if compareSemver(latestTag, current) > 0 {
		return latestTag
	}
	return ""
}

// compareSemver compares two dotted versions, ignoring a leading "v" and any
// build metadata. Returns -1 if a<b, 0 if equal, 1 if a>b. The numeric core
// (major.minor.patch) is compared numerically; a release outranks a prerelease
// of the same core (1.0.0 > 1.0.0-rc1); two prereleases fall back to a string
// compare. This is sufficient for goreleaser's clean release tags; "dev"/
// "ahead"/"dirty" builds never reach here (filtered by isDevBuild).
func compareSemver(a, b string) int {
	ac, ap := splitVersion(a)
	bc, bp := splitVersion(b)
	for i := 0; i < 3; i++ {
		if ac[i] != bc[i] {
			if ac[i] < bc[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case ap == bp:
		return 0
	case ap == "": // a is a release, b a prerelease of the same core → a newer
		return 1
	case bp == "":
		return -1
	case ap < bp:
		return -1
	default:
		return 1
	}
}

// splitVersion parses "v1.2.3-rc1+build" into its numeric core [1,2,3] and
// prerelease ("rc1"). Missing core components default to 0; non-numeric ones
// are treated as 0.
func splitVersion(v string) ([3]int, string) {
	v = strings.TrimPrefix(v, "v")
	core, pre := v, ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		core, pre = v[:i], v[i+1:]
	}
	// Drop any build metadata (from the core if there was no prerelease, or
	// from the tail of the prerelease).
	if j := strings.IndexByte(core, '+'); j >= 0 {
		core = core[:j]
	}
	if j := strings.IndexByte(pre, '+'); j >= 0 {
		pre = pre[:j]
	}
	var nums [3]int
	for i, p := range strings.SplitN(core, ".", 3) {
		nums[i], _ = strconv.Atoi(p)
	}
	return nums, pre
}

// fetchTagWithRetry calls a tool's fetchTag seam up to twice, pausing briefly
// between attempts. Returns "" if both attempts fail or yield an empty tag.
func fetchTagWithRetry(fetch func() (string, error)) string {
	for attempt := 1; attempt <= 2; attempt++ {
		if tag, err := fetch(); err == nil && tag != "" {
			return tag
		}
		if attempt < 2 {
			time.Sleep(updateCheckRetryDelay)
		}
	}
	return ""
}

// raptorSemver extracts the numeric version from `raptor --version` output
// (e.g. "raptor version 0.1.81").
var raptorSemver = regexp.MustCompile(`\d+\.\d+\.\d+`)

// execRaptorVersion returns raptor's local version and whether raptor is
// installed, by running `raptor --version`. Not on PATH or unparsable →
// ("", false), so the engine reports it not-installed and never stale.
func execRaptorVersion() (string, bool) {
	if _, err := exec.LookPath("raptor"); err != nil {
		return "", false
	}
	out, err := exec.Command("raptor", "--version").Output()
	if err != nil {
		return "", false
	}
	v := raptorSemver.FindString(string(out))
	return v, v != ""
}

// readFreshnessCache reads the per-tool throttle cache from disk.
func readFreshnessCache() (freshnessCache, error) {
	path, err := paths.UpdateCheckCache()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c freshnessCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return c, nil
}

// saveFreshnessCache persists the whole cache, creating ~/.praxis if needed.
func saveFreshnessCache(c freshnessCache) error {
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

// putCacheEntry merges one tool's entry into the shared cache (best-effort).
func putCacheEntry(tool string, e toolCacheEntry) {
	c, err := readFreshnessCache()
	if err != nil || c == nil {
		c = freshnessCache{}
	}
	c[tool] = e
	_ = saveFreshnessCache(c)
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
// this invocation. Suppressed for the version-printing flags and for the
// version/update/completion commands (which already report version state, or —
// for completion — emit shell-sourced stdout where stray stderr would surprise
// users). Only the leading version flags and the first positional token (the
// command) are inspected, so a positional value that merely happens to be named
// like a command — e.g. `praxis login --profile update` — does not suppress it.
func skipUpdateCheck(args []string) bool {
	for _, a := range args {
		switch {
		case a == "--version" || a == "-v":
			return true
		case strings.HasPrefix(a, "-"):
			continue // a flag (or its value) before the command — keep scanning
		default:
			// First positional token is the command name.
			return a == "update" || a == "version" || a == "completion"
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
