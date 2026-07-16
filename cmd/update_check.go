package cmd

import (
	"context"
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

// freshMode controls whether checkTool may hit the network.
type freshMode int

const (
	// freshCached reads the cache only — NO network. Used by `praxis status`,
	// which is a local-only snapshot; a cache miss yields "" (not stale).
	freshCached freshMode = iota
	// freshCachedOrFetch serves a <24h cache entry, else fetches once. Used by
	// the Execute nag and login.
	freshCachedOrFetch
	// freshLive always fetches. Used by `praxis status --refresh`.
	freshLive
)

// checkTool resolves one tool's freshness: best-effort, never fatal. The
// kill-switch and non-checkable tools (praxis dev build / raptor absent)
// short-circuit to "not stale".
func checkTool(spec toolSpec, now time.Time, mode freshMode) Freshness {
	cur, installed, checkable := spec.current()
	f := Freshness{Tool: spec.Name, Installed: installed, Current: cur}
	if os.Getenv("PRAXIS_NO_UPDATE_CHECK") != "" || !checkable {
		return f
	}
	f.Latest = latestTagFor(spec, now, mode)
	f.Stale = f.Latest != "" && compareSemver(cur, f.Latest) < 0
	return f
}

// checkForUpdate preserves praxis's original nag entry point: the latest tag
// when praxis itself is behind, else "". Now a thin call over the shared engine.
func checkForUpdate() string {
	if f := checkTool(praxisSpec(), time.Now(), freshCachedOrFetch); f.Stale {
		return f.Latest
	}
	return ""
}

// toolsFreshness reports every registered tool's freshness in one pass — the
// source for `praxis status`'s "tools" block. mode is freshCached for a plain
// status (local-only) and freshLive for `--refresh`.
func toolsFreshness(now time.Time, mode freshMode) []Freshness {
	specs := freshnessTools()
	out := make([]Freshness, 0, len(specs))
	for _, spec := range specs {
		out = append(out, checkTool(spec, now, mode))
	}
	return out
}

// latestTagFor returns a tool's latest tag per mode. A failed fetch caches an
// empty tag so an offline/API outage honors the 24h throttle (compareSemver
// treats "" as not-stale) instead of re-fetching every run.
func latestTagFor(spec toolSpec, now time.Time, mode freshMode) string {
	if mode != freshLive {
		if c, err := readFreshnessCache(); err == nil {
			if e, ok := c[spec.Name]; ok && (mode == freshCached || now.Sub(e.CheckedAt) < updateCheckInterval) {
				return e.LatestVersion
			}
		}
	}
	if mode == freshCached {
		return "" // status default: local-only, never fetch
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

// raptorVersionTimeout bounds the local `raptor --version` call so a wedged
// raptor binary can never hang a login/nag freshness check.
var raptorVersionTimeout = 2 * time.Second

// execRaptorVersion returns raptor's local version and whether raptor is
// installed, by running `raptor --version` under a short timeout. Not on PATH
// (the command errors), timed out, or unparsable → ("", false), so the engine
// reports it not-installed and never stale.
func execRaptorVersion() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), raptorVersionTimeout)
	defer cancel()
	out, err := raptorVersionCmd(ctx)
	if err != nil {
		return "", false
	}
	v := raptorSemver.FindString(string(out))
	return v, v != ""
}

// raptorVersionCmd runs `raptor --version` under ctx — a seam so tests can
// simulate a hung binary.
var raptorVersionCmd = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "raptor", "--version").Output()
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

// saveFreshnessCache persists the whole cache ATOMICALLY (temp file + rename),
// creating ~/.praxis if needed. The atomic rename means a concurrent praxis
// process never reads a torn/half-written cache. Concurrent merges are
// last-writer-wins, which is benign for a throttle cache: the worst case is one
// process's fresh entry being overwritten, costing at most one extra GitHub
// call before the next write settles — no corruption, no lost correctness.
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
	tmp, err := os.CreateTemp(dir, ".freshness-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
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

// staleNag pairs a stale tool's freshness with its rendered upgrade line.
type staleNag struct {
	Freshness Freshness
	Action    string
}

// freshnessDeadline bounds a freshness pass (nag/login): a tool whose check
// doesn't finish in time is simply omitted, so a slow raptor lookup never
// suppresses a fast/cached praxis nag nor stalls login.
var freshnessDeadline = 4 * time.Second

// checkToolsBounded runs each tool's check CONCURRENTLY and returns those that
// finish within freshnessDeadline. This bounds aggregate latency and stops one
// slow tool from blocking the others (findings: login latency, nag suppression).
// A tool that misses the deadline is omitted (best-effort); its goroutine
// finishes on its own (and warms the cache for next time).
func checkToolsBounded(now time.Time, mode freshMode) []Freshness {
	specs := freshnessTools()
	ch := make(chan Freshness, len(specs))
	for _, spec := range specs {
		go func(s toolSpec) { ch <- checkTool(s, now, mode) }(spec)
	}
	out := make([]Freshness, 0, len(specs))
	timer := time.NewTimer(freshnessDeadline)
	defer timer.Stop()
	for range specs {
		select {
		case f := <-ch:
			out = append(out, f)
		case <-timer.C:
			return out // partial — return whatever completed in time
		}
	}
	return out
}

// collectStaleNags returns the stale tools (concurrent, bounded) with their
// upgrade instruction — the Execute-time TTY nag source.
func collectStaleNags() []staleNag {
	var out []staleNag
	for _, f := range checkToolsBounded(time.Now(), freshCachedOrFetch) {
		if f.Stale {
			out = append(out, staleNag{Freshness: f, Action: nagActionForTool(f.Tool)})
		}
	}
	return out
}

// specByName looks up a tool's spec by name.
func specByName(name string) (toolSpec, bool) {
	for _, s := range freshnessTools() {
		if s.Name == name {
			return s, true
		}
	}
	return toolSpec{}, false
}

// nagAction is the upgrade instruction per tool: praxis self-updates; raptor is
// nudge-only — praxis surfaces staleness but never runs `raptor upgrade`.
func nagAction(spec toolSpec) string {
	if spec.Name == "praxis" {
		return "Run `praxis update` to install the latest version"
	}
	return fmt.Sprintf("Ask your user, then run `%s` (praxis won't run it for you)", spec.UpgradeHint)
}

// nagActionForTool is nagAction keyed by tool name (post-bounded, where we hold
// a Freshness rather than a spec).
func nagActionForTool(name string) string {
	if s, ok := specByName(name); ok {
		return nagAction(s)
	}
	return ""
}

// printFreshnessBox writes the "update available" box for a stale tool to w
// (os.Stderr in production, so it never pollutes a command's parseable stdout
// when an AI host spawns praxis as a subprocess). actionLine is the tool's
// upgrade instruction.
func printFreshnessBox(f Freshness, actionLine string, w io.Writer) {
	line1 := fmt.Sprintf("  ⚡ %s update available: %s → %s", f.Tool, f.Current, f.Latest)
	line2 := "  " + actionLine
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
