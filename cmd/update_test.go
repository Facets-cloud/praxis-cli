package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/selfupdate"
)

// withFakeRelease swaps the package-level seam to return the supplied release
// (or error) for the duration of one test, then restores the original.
func withFakeRelease(t *testing.T, rel *selfupdate.Release, err error) {
	t.Helper()
	orig := fetchLatestRelease
	fetchLatestRelease = func() (*selfupdate.Release, error) { return rel, err }
	t.Cleanup(func() { fetchLatestRelease = orig })
}

func TestUpdateCmd_NoReleasesYet(t *testing.T) {
	withFakeRelease(t, nil, errors.New("no releases published yet"))

	var buf bytes.Buffer
	updateCmd.SetOut(&buf)
	updateCmd.SetErr(&buf)

	err := updateCmd.RunE(updateCmd, nil)
	if err == nil {
		t.Fatal("expected error when no releases exist, got nil")
	}
	if !strings.Contains(err.Error(), "no releases") {
		t.Errorf("err = %v, want substring 'no releases'", err)
	}
}

func TestUpdateCmd_AlreadyOnLatest(t *testing.T) {
	// Pretend the upstream's latest tag matches our build version.
	withFakeRelease(t, &selfupdate.Release{TagName: "v" + version}, nil)

	var buf bytes.Buffer
	updateCmd.SetOut(&buf)
	updateJSON = false

	if err := updateCmd.RunE(updateCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	// bytes.Buffer is non-TTY → render auto-emits JSON. Check the
	// structured shape rather than the human string.
	out := buf.String()
	if !strings.Contains(out, `"reason": "already_latest"`) {
		t.Errorf("output = %q, want JSON with reason=already_latest", out)
	}
	if !strings.Contains(out, `"updated": false`) {
		t.Errorf("output = %q, want updated=false", out)
	}
}

func TestUpdateCmd_NoMatchingAsset(t *testing.T) {
	// A newer release exists but has no asset for our OS/arch.
	withFakeRelease(t, &selfupdate.Release{
		TagName: "v999.0.0",
		Assets:  []selfupdate.Asset{{Name: "praxis_solaris_sparc"}},
	}, nil)

	var buf bytes.Buffer
	updateCmd.SetOut(&buf)
	updateCmd.SetErr(&buf)

	err := updateCmd.RunE(updateCmd, nil)
	if err == nil {
		t.Fatal("expected error when no asset matches platform, got nil")
	}
}

// withSelfPath stands the running-binary path in a staged layout for one test.
func withSelfPath(t *testing.T, path string) {
	t.Helper()
	orig := selfPath
	selfPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { selfPath = orig })
}

// newerRelease is a release with an asset for this platform, so a test reaches
// the code after AssetForPlatform.
func newerRelease() *selfupdate.Release {
	return &selfupdate.Release{
		TagName: "v999.0.0",
		Assets: []selfupdate.Asset{
			{Name: "praxis_" + runtime.GOOS + "_" + runtime.GOARCH},
			{Name: "checksums.txt"},
		},
	}
}

// A self-update into Homebrew's tree desyncs brew's recorded version from the
// file on disk, and (before TargetPath) destroyed brew's bin/ symlink. Refuse
// and name `brew upgrade` instead — and download nothing.
func TestUpdateCmd_RefusesAHomebrewInstall(t *testing.T) {
	withFakeRelease(t, newerRelease(), nil)

	dir := t.TempDir()
	caskDir := filepath.Join(dir, "Caskroom", "praxis", "1.8.1")
	if err := os.MkdirAll(caskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(caskDir, "praxis_darwin_arm64")
	if err := os.WriteFile(real, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(binDir, "praxis")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	withSelfPath(t, link) // invoked through brew's link, as a real user is

	downloaded := false
	origDL := downloadAsset
	downloadAsset = func(url string) (string, error) { downloaded = true; return "", errors.New("must not download") }
	t.Cleanup(func() { downloadAsset = origDL })

	var buf bytes.Buffer
	updateCmd.SetOut(&buf)
	updateJSON = false

	if err := updateCmd.RunE(updateCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"reason": "homebrew_managed"`) {
		t.Errorf("output = %q, want reason=homebrew_managed", out)
	}
	if !strings.Contains(out, "brew upgrade --cask praxis") {
		t.Errorf("output = %q, want the brew command", out)
	}
	if downloaded {
		t.Error("downloaded an asset for a Homebrew install; it must refuse first")
	}
	// The link must be untouched — destroying it is the bug being fixed.
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("brew's symlink was replaced by a regular file")
	}
}

// A non-Homebrew install still updates, and targets the real file behind any
// symlink rather than the link.
func TestUpdateCmd_ResolvesTheLinkForANonBrewInstall(t *testing.T) {
	withFakeRelease(t, newerRelease(), nil)

	dir := t.TempDir()
	real := filepath.Join(dir, "praxis-1.8.1")
	if err := os.WriteFile(real, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "praxis")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	withSelfPath(t, link)

	var replacedTarget string
	origAR, origDL, origFT, origVC, origPC := atomicReplace, downloadAsset, fetchTextBody, verifyChecksum, parseChecksums
	atomicReplace = func(cur, new string) error { replacedTarget = cur; return nil }
	downloadAsset = func(url string) (string, error) { return filepath.Join(dir, "dl"), nil }
	fetchTextBody = func(url string) (string, error) { return "sum  asset", nil }
	parseChecksums = func(body, name string) (string, error) { return "sum", nil }
	verifyChecksum = func(path, want string) error { return nil }
	t.Cleanup(func() {
		atomicReplace, downloadAsset, fetchTextBody, verifyChecksum, parseChecksums = origAR, origDL, origFT, origVC, origPC
	})

	var buf bytes.Buffer
	updateCmd.SetOut(&buf)
	updateJSON = false

	if err := updateCmd.RunE(updateCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	want, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if replacedTarget != want {
		t.Fatalf("replaced %q, want the real file %q (never the link)", replacedTarget, want)
	}
}
