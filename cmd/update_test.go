package cmd

import (
	"bytes"
	"errors"
	"io"
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
// file on disk, and (before TargetPath) destroyed brew's bin/ symlink. Without a
// brew that can do the update, name `brew upgrade` instead — and download
// nothing.
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
	downloadAsset = func(string, string) (string, error) { downloaded = true; return "", errors.New("must not download") }
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
	downloadAsset = func(string, string) (string, error) { return filepath.Join(dir, "dl"), nil }
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
	downloadAsset = func(string, string) (string, error) { return tmp, nil }
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

// skipIfRoot skips a test that needs a read-only file or folder: root ignores
// file modes.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes, so a read-only layout cannot be simulated")
	}
}

// readOnlyInstall puts the running praxis at dir/praxis in a read-only folder,
// with the given file mode.
func readOnlyInstall(t *testing.T, mode os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	self := filepath.Join(dir, "praxis")
	if err := os.WriteFile(self, []byte("old"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	withSelfPath(t, self)
	return self
}

// withWriteInPlace sets selfupdate.WriteInPlaceSafe for one test.
func withWriteInPlace(t *testing.T, safe bool) {
	t.Helper()
	orig := selfupdate.WriteInPlaceSafe
	selfupdate.WriteInPlaceSafe = safe
	t.Cleanup(func() { selfupdate.WriteInPlaceSafe = orig })
}

// An install that praxis cannot replace is refused before the download, with
// the reason and the fixes.
func TestUpdateCmd_RefusesWhatItCannotReplace(t *testing.T) {
	skipIfRoot(t)
	tests := []struct {
		name string
		safe bool
		mode os.FileMode
		want string
	}{
		{"read-only file", true, 0o555, "you cannot write to this file or to its folder"},
		{"no write in place on Linux", false, 0o755, "praxis cannot change its own file while it runs"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withFakeRelease(t, newerRelease(), nil)
			withWriteInPlace(t, tc.safe)
			self := readOnlyInstall(t, tc.mode)
			origDL := downloadAsset
			downloadAsset = func(string, string) (string, error) { t.Fatal("must not download"); return "", nil }
			t.Cleanup(func() { downloadAsset = origDL })

			var buf bytes.Buffer
			updateCmd.SetOut(&buf)
			err := updateCmd.RunE(updateCmd, nil)
			real, _ := filepath.EvalSymlinks(self)
			if err == nil || !strings.Contains(err.Error(), "praxis cannot update "+real) ||
				!strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "sudo praxis update") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// When the rename fails but the user may write the file (macOS), the update
// hands the write to a temporary copy of praxis.
func TestUpdateCmd_HandsTheWriteToAHelper(t *testing.T) {
	skipIfRoot(t)
	withFakeRelease(t, newerRelease(), nil)
	withWriteInPlace(t, true)
	self := readOnlyInstall(t, 0o755)
	tmp := filepath.Join(t.TempDir(), "dl")
	if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	var gotDir string
	var handed []string
	origDL, origFT, origPC, origVC, origH := downloadAsset, fetchTextBody, parseChecksums, verifyChecksum, handOffUpdate
	downloadAsset = func(_, dir string) (string, error) { gotDir = dir; return tmp, nil }
	fetchTextBody = func(string) (string, error) { return "sum  asset", nil }
	parseChecksums = func(string, string) (string, error) { return "sum", nil }
	verifyChecksum = func(string, string) error { return nil }
	handOffUpdate = func(tmpPath, target, from, to string, asJSON bool) error {
		handed = []string{tmpPath, target, from, to}
		return nil
	}
	t.Cleanup(func() {
		downloadAsset, fetchTextBody, parseChecksums, verifyChecksum, handOffUpdate = origDL, origFT, origPC, origVC, origH
	})

	var buf bytes.Buffer
	updateCmd.SetOut(&buf)
	if err := updateCmd.RunE(updateCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	real, _ := filepath.EvalSymlinks(self)
	if gotDir != filepath.Dir(real) {
		t.Errorf("download dir = %q, want the binary's folder %q", gotDir, filepath.Dir(real))
	}
	if len(handed) != 4 || handed[0] != tmp || handed[1] != real || handed[3] != "999.0.0" {
		t.Errorf("handed off %v", handed)
	}
}

// writeHelper writes a helper file named like startUpdateHelper's.
func writeHelper(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// The helper writes the download into the binary in place, removes the
// download and itself, and finishes the update.
func TestFinishUpdate_WritesInPlaceAndFinishes(t *testing.T) {
	bin := writeHelper(t, "praxis", "old")
	before, _ := os.Stat(bin)
	dl := writeHelper(t, "dl", "new")
	self := writeHelper(t, updateHelperPrefix+"1", "old")

	var buf bytes.Buffer
	if err := finishUpdate(&buf, self, []string{dl, bin, "1.0.0", "2.0.0", "text"}); err != nil {
		t.Fatalf("finishUpdate: %v", err)
	}
	got, _ := os.ReadFile(bin)
	after, _ := os.Stat(bin)
	if string(got) != "new" || !os.SameFile(before, after) {
		t.Errorf("binary = %q, same file %t; want new, written in place", got, os.SameFile(before, after))
	}
	for _, p := range []string{dl, self} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still exists", p)
		}
	}
	if !strings.Contains(buf.String(), "Updated to 2.0.0") {
		t.Errorf("output = %q", buf.String())
	}
}

// Helper mode never runs from an installed praxis or with the wrong arguments,
// and a missing download leaves the binary as it was.
func TestFinishUpdate_Refusals(t *testing.T) {
	bin := writeHelper(t, "praxis", "old")
	helper := writeHelper(t, updateHelperPrefix+"1", "old")
	tests := []struct {
		name, self string
		args       []string
		want       string
	}{
		{"wrong arguments", helper, []string{"x"}, "is for praxis update only"},
		{"installed binary", bin, []string{"a", bin, "1", "2", "text"}, "is for praxis update only"},
		{"missing download", helper, []string{filepath.Join(t.TempDir(), "none"), bin, "1", "2", "text"}, "install:"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := finishUpdate(io.Discard, tc.self, tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if got, _ := os.ReadFile(bin); string(got) != "old" {
				t.Errorf("binary = %q, want it unchanged", got)
			}
		})
	}
}

// brewInstall stages praxis the way a Homebrew cask does, under a prefix with
// an executable bin/brew, and returns the prefix.
func brewInstall(t *testing.T, token string) string {
	t.Helper()
	prefix := t.TempDir()
	caskDir := filepath.Join(prefix, "Caskroom", token, "1.0.0")
	for _, d := range []string{caskDir, filepath.Join(prefix, "bin")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	real := filepath.Join(caskDir, "praxis_darwin_arm64")
	if err := os.WriteFile(real, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prefix, "bin", "brew"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	withSelfPath(t, real)
	return prefix
}

// A Homebrew install is updated by its own brew: `brew update`, then `brew
// upgrade --cask <token>`, with no download by praxis.
func TestUpdateCmd_UpdatesAHomebrewInstallWithBrew(t *testing.T) {
	withFakeRelease(t, newerRelease(), nil)
	prefix := brewInstall(t, "praxis-test")
	var calls []string
	origB, origDL := runBrew, downloadAsset
	runBrew = func(_ io.Writer, brew string, args ...string) error {
		calls = append(calls, brew+" "+strings.Join(args, " "))
		return nil
	}
	downloadAsset = func(string, string) (string, error) { t.Fatal("must not download"); return "", nil }
	t.Cleanup(func() { runBrew, downloadAsset = origB, origDL })

	var buf bytes.Buffer
	updateCmd.SetOut(&buf)
	if err := updateCmd.RunE(updateCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	realPrefix, _ := filepath.EvalSymlinks(prefix)
	brew := filepath.Join(realPrefix, "bin", "brew")
	want := []string{brew + " update --quiet", brew + " upgrade --cask praxis-test"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Errorf("brew calls = %q, want %q", calls, want)
	}
	if !strings.Contains(buf.String(), `"via": "homebrew"`) {
		t.Errorf("output = %q, want via=homebrew", buf.String())
	}
}

// A failed brew step fails the update and shows brew's output.
func TestUpdateCmd_ReportsABrewFailure(t *testing.T) {
	withFakeRelease(t, newerRelease(), nil)
	brewInstall(t, "praxis")
	origB := runBrew
	runBrew = func(w io.Writer, _ string, args ...string) error {
		if args[0] == "upgrade" {
			_, _ = io.WriteString(w, "Error: praxis: download failed")
			return errors.New("exit status 1")
		}
		return nil
	}
	t.Cleanup(func() { runBrew = origB })

	var buf bytes.Buffer
	updateCmd.SetOut(&buf)
	err := updateCmd.RunE(updateCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "brew upgrade --cask praxis") || !strings.Contains(err.Error(), "download failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestBrewFor(t *testing.T) {
	skipIfRoot(t)
	prefix := t.TempDir()
	caskDir := filepath.Join(prefix, "Caskroom", "praxis", "1.0.0")
	if err := os.MkdirAll(caskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	brew := filepath.Join(prefix, "bin", "brew")
	if b, tok := brewFor(caskDir); b != "" || tok != "" {
		t.Errorf("missing brew: got %q %q", b, tok)
	}
	if err := os.MkdirAll(filepath.Dir(brew), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brew, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := brewFor(caskDir); b != "" {
		t.Errorf("brew that cannot run: got %q", b)
	}
	if err := os.Chmod(brew, 0o755); err != nil {
		t.Fatal(err)
	}
	if b, tok := brewFor(caskDir); b != brew || tok != "praxis" {
		t.Errorf("got %q %q, want %q praxis", b, tok, brew)
	}
	tokenDir := filepath.Dir(caskDir)
	if err := os.Chmod(tokenDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(tokenDir, 0o755) })
	if b, _ := brewFor(caskDir); b != "" {
		t.Errorf("read-only cask folder: got %q", b)
	}
}

// execBrew turns off brew's own auto-update, cleanup and hints.
func TestExecBrewEnvironment(t *testing.T) {
	brew := writeHelper(t, "brew", "#!/bin/sh\necho \"$HOMEBREW_NO_AUTO_UPDATE$HOMEBREW_NO_INSTALL_CLEANUP$HOMEBREW_NO_ENV_HINTS $*\"\n")
	var buf bytes.Buffer
	if err := execBrew(&buf, brew, "upgrade", "--cask", "praxis"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != "111 upgrade --cask praxis" {
		t.Errorf("brew saw %q", got)
	}
}

// Without its binary the helper cannot start, and it leaves no helper file.
func TestStartUpdateHelper_FailsWithoutTheBinary(t *testing.T) {
	before, _ := filepath.Glob(filepath.Join(os.TempDir(), updateHelperPrefix+"*"))
	err := startUpdateHelper("dl", filepath.Join(t.TempDir(), "missing"), "1", "2", false)
	if err == nil || !strings.Contains(err.Error(), "install:") {
		t.Fatalf("err = %v", err)
	}
	after, _ := filepath.Glob(filepath.Join(os.TempDir(), updateHelperPrefix+"*"))
	if len(after) > len(before) {
		t.Errorf("helper files left: %v", after)
	}
}

// The helper keeps the caller's output format, and a write that cannot happen
// names the failed restore too.
func TestFinishUpdate_JSONAndFailedRestore(t *testing.T) {
	bin := writeHelper(t, "praxis", "old")
	var buf bytes.Buffer
	args := []string{writeHelper(t, "dl", "new"), bin, "1.0.0", "2.0.0", "json"}
	if err := finishUpdate(&buf, writeHelper(t, updateHelperPrefix+"1", "old"), args); err != nil {
		t.Fatalf("finishUpdate: %v", err)
	}
	if !strings.Contains(buf.String(), `"to_version": "2.0.0"`) {
		t.Errorf("output = %q, want JSON", buf.String())
	}

	skipIfRoot(t)
	if err := os.Chmod(bin, 0o444); err != nil {
		t.Fatal(err)
	}
	args = []string{writeHelper(t, "dl", "new"), bin, "1.0.0", "2.0.0", "text"}
	err := finishUpdate(io.Discard, writeHelper(t, updateHelperPrefix+"2", "old"), args)
	if err == nil || !strings.Contains(err.Error(), "restoring the old binary also failed") {
		t.Fatalf("err = %v", err)
	}
}

// A person sees brew's output and a short success line.
func TestBrewUpdate_TextOutput(t *testing.T) {
	origB := runBrew
	runBrew = func(w io.Writer, _ string, args ...string) error {
		_, _ = io.WriteString(w, "brew "+args[0]+"\n")
		return nil
	}
	t.Cleanup(func() { runBrew = origB })
	var buf bytes.Buffer
	if err := brewUpdate(&buf, false, "/x/bin/brew", "praxis", "1.0.0", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"brew update", "brew upgrade", "Updated with Homebrew (praxis)"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output = %q, want %q", buf.String(), want)
		}
	}
}

// The start-up clean-up never fails a command, on any platform.
func TestCleanupOldBinariesIsSafe(t *testing.T) {
	cleanupOldBinaries()
}
