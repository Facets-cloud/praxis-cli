package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/selfupdate"
)

// withVersion temporarily overrides the build-stamped version var.
func withVersion(t *testing.T, v string) {
	t.Helper()
	orig := version
	version = v
	t.Cleanup(func() { version = orig })
}

// fakeHome redirects $HOME to a temp dir so paths.UpdateCheckCache() and the
// cache writes land in isolation, never the developer's real ~/.praxis.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// writeCache writes a throttle cache with the given age and latest version.
func writeCache(t *testing.T, age time.Duration, latest string) {
	t.Helper()
	if err := saveUpdateCache(updateCheckCache{
		CheckedAt:     time.Now().Add(-age),
		LatestVersion: latest,
	}); err != nil {
		t.Fatalf("saveUpdateCache: %v", err)
	}
}

func TestCheckForUpdate(t *testing.T) {
	// Zero the retry delay so the error path doesn't sleep.
	origDelay := updateCheckRetryDelay
	updateCheckRetryDelay = 0
	t.Cleanup(func() { updateCheckRetryDelay = origDelay })

	tests := []struct {
		name      string
		version   string
		noCheck   bool                // set PRAXIS_NO_UPDATE_CHECK
		rel       *selfupdate.Release // live-fetch result
		relErr    error               // live-fetch error
		seedCache func(t *testing.T)  // pre-populate the on-disk cache
		failFetch bool                // make the fetch seam panic if called
		want      string
	}{
		{
			name:    "behind returns latest tag",
			version: "1.0.0",
			rel:     &selfupdate.Release{TagName: "v1.2.0"},
			want:    "v1.2.0",
		},
		{
			name:    "newer patch returns latest tag",
			version: "1.0.0",
			rel:     &selfupdate.Release{TagName: "v1.0.1"},
			want:    "v1.0.1",
		},
		{
			name:    "equal returns empty",
			version: "1.2.0",
			rel:     &selfupdate.Release{TagName: "v1.2.0"},
			want:    "",
		},
		{
			name:    "equal without v-prefix on tag",
			version: "v1.2.0",
			rel:     &selfupdate.Release{TagName: "1.2.0"},
			want:    "",
		},
		{
			// Regression: installed binary is NEWER than the live "latest".
			// String-equality would nag backward; semver must stay silent.
			name:    "installed newer than latest stays silent",
			version: "1.2.0",
			rel:     &selfupdate.Release{TagName: "v1.0.0"},
			want:    "",
		},
		{
			name:    "dev build skipped",
			version: "dev",
			rel:     &selfupdate.Release{TagName: "v9.9.9"},
			want:    "",
		},
		{
			name:    "git-describe ahead build skipped",
			version: "v1.2.0-3-gabc1234",
			rel:     &selfupdate.Release{TagName: "v9.9.9"},
			want:    "",
		},
		{
			name:    "dirty build skipped",
			version: "v1.2.0-dirty",
			rel:     &selfupdate.Release{TagName: "v9.9.9"},
			want:    "",
		},
		{
			name:    "no-check env var skips",
			version: "1.0.0",
			noCheck: true,
			rel:     &selfupdate.Release{TagName: "v9.9.9"},
			want:    "",
		},
		{
			name:    "fetch error stays silent",
			version: "1.0.0",
			relErr:  errors.New("network down"),
			want:    "",
		},
		{
			name:    "fresh cache served without fetching",
			version: "1.0.0",
			seedCache: func(t *testing.T) {
				writeCache(t, time.Hour, "v2.0.0") // 1h old < 24h
			},
			failFetch: true, // proves the live fetch is not consulted
			want:      "v2.0.0",
		},
		{
			name:    "fresh cache equal to current returns empty",
			version: "2.0.0",
			seedCache: func(t *testing.T) {
				writeCache(t, time.Hour, "v2.0.0")
			},
			failFetch: true,
			want:      "",
		},
		{
			// Exact production bug: a fresh cache holds the tag that was latest
			// when written (v1.0.0), but the user has since self-upgraded to
			// 1.1.0 within the 24h window. Must NOT nag backward.
			name:    "fresh cache older than installed stays silent",
			version: "1.1.0",
			seedCache: func(t *testing.T) {
				writeCache(t, time.Hour, "v1.0.0")
			},
			failFetch: true,
			want:      "",
		},
		{
			name:    "stale cache triggers fetch",
			version: "1.0.0",
			seedCache: func(t *testing.T) {
				writeCache(t, 48*time.Hour, "v1.5.0") // stale
			},
			rel:  &selfupdate.Release{TagName: "v3.0.0"},
			want: "v3.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeHome(t)
			withVersion(t, tt.version)
			if tt.noCheck {
				t.Setenv("PRAXIS_NO_UPDATE_CHECK", "1")
			} else {
				t.Setenv("PRAXIS_NO_UPDATE_CHECK", "")
			}
			if tt.seedCache != nil {
				tt.seedCache(t)
			}

			orig := fetchLatestRelease
			t.Cleanup(func() { fetchLatestRelease = orig })
			if tt.failFetch {
				fetchLatestRelease = func() (*selfupdate.Release, error) {
					t.Error("live fetch should not be called when cache is fresh")
					return nil, errors.New("unexpected fetch")
				}
			} else {
				fetchLatestRelease = func() (*selfupdate.Release, error) {
					return tt.rel, tt.relErr
				}
			}

			if got := checkForUpdate(); got != tt.want {
				t.Errorf("checkForUpdate() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCheckForUpdate_PersistsCache verifies a live fetch writes the throttle
// cache so the next call within the interval is served from disk.
func TestCheckForUpdate_PersistsCache(t *testing.T) {
	fakeHome(t)
	withVersion(t, "1.0.0")
	t.Setenv("PRAXIS_NO_UPDATE_CHECK", "")

	orig := fetchLatestRelease
	t.Cleanup(func() { fetchLatestRelease = orig })
	fetchLatestRelease = func() (*selfupdate.Release, error) {
		return &selfupdate.Release{TagName: "v4.0.0"}, nil
	}

	if got := checkForUpdate(); got != "v4.0.0" {
		t.Fatalf("first call = %q, want v4.0.0", got)
	}

	path, err := paths.UpdateCheckCache()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	var c updateCheckCache
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("cache not valid JSON: %v", err)
	}
	if c.LatestVersion != "v4.0.0" {
		t.Errorf("cached LatestVersion = %q, want v4.0.0", c.LatestVersion)
	}
	if time.Since(c.CheckedAt) > time.Minute {
		t.Errorf("cached CheckedAt = %v, want ~now", c.CheckedAt)
	}
}

// TestCheckForUpdate_ThrottlesFailures verifies a failed live fetch records the
// attempt (empty LatestVersion) so the 24h throttle applies offline: the next
// call within the interval is served from cache and does not re-fetch.
func TestCheckForUpdate_ThrottlesFailures(t *testing.T) {
	origDelay := updateCheckRetryDelay
	updateCheckRetryDelay = 0
	t.Cleanup(func() { updateCheckRetryDelay = origDelay })

	fakeHome(t)
	withVersion(t, "1.0.0")
	t.Setenv("PRAXIS_NO_UPDATE_CHECK", "")

	orig := fetchLatestRelease
	t.Cleanup(func() { fetchLatestRelease = orig })

	// First call: fetch fails → should persist a throttle marker and stay silent.
	calls := 0
	fetchLatestRelease = func() (*selfupdate.Release, error) {
		calls++
		return nil, errors.New("offline")
	}
	if got := checkForUpdate(); got != "" {
		t.Fatalf("first call = %q, want empty", got)
	}
	firstCalls := calls
	if firstCalls == 0 {
		t.Fatal("expected the first call to attempt a live fetch")
	}

	c, err := readUpdateCache()
	if err != nil {
		t.Fatalf("throttle marker not written: %v", err)
	}
	if c.LatestVersion != "" {
		t.Errorf("LatestVersion = %q, want empty on a failed fetch", c.LatestVersion)
	}

	// Second call within the interval: must be served from cache, no re-fetch.
	if got := checkForUpdate(); got != "" {
		t.Fatalf("second call = %q, want empty", got)
	}
	if calls != firstCalls {
		t.Errorf("second call re-fetched (%d → %d); throttle not honored", firstCalls, calls)
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"v1.0.0", "1.0.0", 0}, // v-prefix ignored
		{"1.1.0", "1.0.0", 1},
		{"1.0.0", "1.1.0", -1},
		{"1.0.1", "1.0.0", 1},
		{"2.0.0", "1.9.9", 1},
		{"1.10.0", "1.9.0", 1},        // numeric, not lexical
		{"1.0", "1.0.0", 0},           // missing patch defaults to 0
		{"1.0.0", "1.0.0-rc1", 1},     // release outranks prerelease
		{"1.0.0-rc1", "1.0.0", -1},    // and vice versa
		{"1.0.0-rc2", "1.0.0-rc1", 1}, // prerelease string compare
		{"1.2.3+build", "1.2.3", 0},   // build metadata ignored
	}
	for _, tt := range tests {
		if got := compareSemver(tt.a, tt.b); got != tt.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestNewerThan(t *testing.T) {
	tests := []struct {
		current, latestTag, want string
	}{
		{"1.0.0", "v1.1.0", "v1.1.0"}, // newer → returns the tag verbatim
		{"1.0.0", "v1.0.0", ""},       // equal
		{"1.1.0", "v1.0.0", ""},       // older → silent (the bug)
		{"1.0.0", "", ""},             // empty (failed-fetch marker)
	}
	for _, tt := range tests {
		if got := newerThan(tt.current, tt.latestTag); got != tt.want {
			t.Errorf("newerThan(%q, %q) = %q, want %q", tt.current, tt.latestTag, got, tt.want)
		}
	}
}

func TestPrintUpdateNotification(t *testing.T) {
	withVersion(t, "1.0.0")
	var buf bytes.Buffer
	printUpdateNotification("v1.2.0", &buf)
	out := buf.String()

	for _, want := range []string{"1.0.0", "v1.2.0", "praxis update", "PRAXIS_NO_UPDATE_CHECK"} {
		if !strings.Contains(out, want) {
			t.Errorf("notification missing %q\n%s", want, out)
		}
	}

	// Every box line must be the same display width so the right border lines
	// up — this is what the wide-rune handling exists for (⚡ and → are wide).
	var widths []int
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		widths = append(widths, displayWidth(line))
	}
	for i, w := range widths {
		if w != widths[0] {
			t.Errorf("line %d display width = %d, want %d (misaligned box)\n%s", i, w, widths[0], out)
		}
	}
}

func TestSkipUpdateCheck(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{[]string{"status"}, false},
		{[]string{"login", "--profile", "x"}, false},
		{[]string{"update"}, true},
		{[]string{"version"}, true},
		{[]string{"--version"}, true},
		{[]string{"-v"}, true},
		{[]string{"completion", "zsh"}, true},
		// A positional value named like a command must NOT suppress the check.
		{[]string{"login", "--profile", "update"}, false},
		{[]string{"status", "version"}, false},
		{nil, false},
	}
	for _, tt := range tests {
		if got := skipUpdateCheck(tt.args); got != tt.want {
			t.Errorf("skipUpdateCheck(%v) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

func TestIsDevBuild(t *testing.T) {
	tests := []struct {
		v    string
		want bool
	}{
		{"1.0.0", false},            // goreleaser-stamped release
		{"v1.0.0", false},           // on-tag local build
		{"", true},                  // unstamped
		{"dev", true},               // no-tag fallback
		{"v1.0.0-dev", true},        // explicit dev suffix
		{"v1.0.0-dirty", true},      // modified tree
		{"v1.0.0-3-gabc1234", true}, // ahead of tag
		{"v1.0.0-rc1", false},       // legit prerelease — must still nag
		{"v2.0.0-beta.1", false},    // legit prerelease
	}
	for _, tt := range tests {
		if got := isDevBuild(tt.v); got != tt.want {
			t.Errorf("isDevBuild(%q) = %v, want %v", tt.v, got, tt.want)
		}
	}
}

func TestRuneWidth(t *testing.T) {
	tests := []struct {
		r    rune
		want int
	}{
		{'a', 1},
		{' ', 1},
		{'→', 1}, // U+2192 is narrow
		{'⚡', 2}, // U+26A1 wide
		{'世', 2}, // CJK
		{'😀', 2}, // emoji
	}
	for _, tt := range tests {
		if got := runeWidth(tt.r); got != tt.want {
			t.Errorf("runeWidth(%q) = %d, want %d", tt.r, got, tt.want)
		}
	}
}
