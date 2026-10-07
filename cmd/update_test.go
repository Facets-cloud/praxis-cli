package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/clifeed"
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
	t.Setenv("HOME", t.TempDir()) // the update refreshes installed skills
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

// A feed release names the platform as "<os>/<arch>" and carries a SHA-256
// for each asset; the update path reads it in the GitHub shape.
func TestFeedReleaseMapsAssetsAndDigest(t *testing.T) {
	rel := feedRelease(clifeed.Release{Target: "2.1.0", Assets: map[string]clifeed.Asset{
		"darwin/arm64": {URL: "https://example.test/praxis_darwin_arm64", SHA256: "abc"},
		"linux/amd64":  {URL: "https://example.test/praxis_linux_amd64"},
		"bad":          {URL: "https://example.test/x"},
	}})
	if rel.TagName != "v2.1.0" || !strings.HasSuffix(rel.HTMLURL, "/releases/tag/v2.1.0") {
		t.Fatalf("release = %+v", rel)
	}
	byName := map[string]selfupdate.Asset{}
	for _, a := range rel.Assets {
		byName[a.Name] = a
	}
	// The asset without a SHA-256 is dropped.
	if len(byName) != 1 || byName["praxis_darwin_arm64"].Digest != "sha256:abc" {
		t.Errorf("assets = %+v", rel.Assets)
	}
}

// With no checksums.txt, the update verifies the download against the asset's
// digest.
func TestUpdateCmd_VerifiesTheAssetDigest(t *testing.T) {
	rel := &selfupdate.Release{TagName: "v999.0.0", Assets: []selfupdate.Asset{
		{Name: "praxis_" + runtime.GOOS + "_" + runtime.GOARCH, BrowserDownloadURL: "https://example.test/bin", Digest: "sha256:feed01"},
	}}
	withFakeRelease(t, rel, nil)
	self := filepath.Join(t.TempDir(), "praxis")
	if err := os.WriteFile(self, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	withSelfPath(t, self)
	origD, origV := downloadAsset, verifyChecksum
	t.Cleanup(func() { downloadAsset, verifyChecksum = origD, origV })
	tmp := filepath.Join(t.TempDir(), "dl")
	if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	downloadAsset = func(string) (string, error) { return tmp, nil }
	var expected string
	verifyChecksum = func(_, want string) error { expected = want; return errors.New("stop here") }

	updateYes = true
	t.Cleanup(func() { updateYes = false })
	var buf bytes.Buffer
	updateCmd.SetOut(&buf)
	err := updateCmd.RunE(updateCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "stop here") {
		t.Fatalf("err = %v, want the stub's error", err)
	}
	if expected != "feed01" {
		t.Errorf("verified against %q, want the digest", expected)
	}
}
