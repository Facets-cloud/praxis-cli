package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/httpclient"
	"github.com/Facets-cloud/praxis-cli/internal/selfupdate"
)

// autoUpdateEnvFor isolates HOME, turns every gate on, and runs as release
// 2.1.0 from a binary in a writable folder. It returns that binary.
func autoUpdateEnvFor(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range append(httpclient.CIEnvVars(), "CI", autoUpdateEnv, "PRAXIS_NO_UPDATE_CHECK") {
		t.Setenv(k, "")
	}
	bin := filepath.Join(t.TempDir(), "praxis")
	if err := os.WriteFile(bin, []byte("old praxis"), 0o755); err != nil {
		t.Fatal(err)
	}
	origV, origPath, origRoot, origBox, origStart := version, selfPath, autoUpdateAsRoot, autoUpdateInContainer, startAutoUpdate
	t.Cleanup(func() {
		version, selfPath, autoUpdateAsRoot, autoUpdateInContainer, startAutoUpdate = origV, origPath, origRoot, origBox, origStart
	})
	version = "2.1.0"
	selfPath = func() (string, error) { return bin, nil }
	autoUpdateAsRoot = func() bool { return false }
	autoUpdateInContainer = func() bool { return false }
	startAutoUpdate = func(string, string) error { t.Fatal("startAutoUpdate was not stubbed"); return nil }
	return bin
}

func cachePraxisTarget(t *testing.T, tag string) {
	t.Helper()
	putCacheEntry("praxis", toolCacheEntry{CheckedAt: time.Now(), LatestVersion: tag})
}

// fakeCask stages a Homebrew cask of praxis at version and a brew beside it.
func fakeCask(t *testing.T, ver string) string {
	t.Helper()
	prefix := t.TempDir()
	bin := filepath.Join(prefix, "Caskroom", "praxis", ver, "praxis")
	brew := filepath.Join(prefix, "bin", "brew")
	for _, f := range []string{bin, brew} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

func TestAutoUpdateMode(t *testing.T) {
	tests := []struct {
		name, mode, reason string
		set                func(t *testing.T, bin string) string
	}{
		{"rename", "rename", "", func(_ *testing.T, bin string) string { return bin }},
		{"dev build", "off", "dev", func(_ *testing.T, bin string) string { version = "2.1.0-3-gabc1234"; return bin }},
		{"opt-out", "off", "env", func(t *testing.T, bin string) string { t.Setenv(autoUpdateEnv, "1"); return bin }},
		{"no update check", "off", "env", func(t *testing.T, bin string) string { t.Setenv("PRAXIS_NO_UPDATE_CHECK", "1"); return bin }},
		{"CI", "off", "ci", func(t *testing.T, bin string) string { t.Setenv("CI", "true"); return bin }},
		{"container", "off", "container", func(_ *testing.T, bin string) string {
			autoUpdateInContainer = func() bool { return true }
			return bin
		}},
		{"root", "off", "root", func(_ *testing.T, bin string) string { autoUpdateAsRoot = func() bool { return true }; return bin }},
		{"unknown path", "off", "folder", func(*testing.T, string) string { return "" }},
		{"missing folder", "off", "folder", func(t *testing.T, _ string) string { return filepath.Join(t.TempDir(), "gone", "praxis") }},
		{"Homebrew", "brew", "", func(t *testing.T, _ string) string { return fakeCask(t, "2.1.0") }},
		{"Homebrew without brew", "off", "brew", func(t *testing.T, _ string) string {
			bin := fakeCask(t, "2.1.0")
			brew := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(bin)))), "bin", "brew")
			if err := os.Remove(brew); err != nil {
				t.Fatal(err)
			}
			return bin
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if strings.HasPrefix(tc.name, "Homebrew") && runtime.GOOS == "windows" {
				t.Skip("Homebrew does not run on Windows")
			}
			path := tc.set(t, autoUpdateEnvFor(t))
			if mode, reason := autoUpdateMode(path); mode != tc.mode || reason != tc.reason {
				t.Errorf("autoUpdateMode = %q, %q; want %q, %q", mode, reason, tc.mode, tc.reason)
			}
		})
	}
}

func TestMaybeAutoUpdate(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		target   string
		last     *autoUpdateResult
		claimAge time.Duration // 0 = no claim file
		startErr error
		want     bool
	}{
		{"newer release", []string{"status"}, "v2.2.0", nil, 0, nil, true},
		{"same release", []string{"status"}, "v2.1.0", nil, 0, nil, false},
		{"older release", []string{"status"}, "v2.0.2", nil, 0, nil, false},
		{"no check yet", []string{"status"}, "", nil, 0, nil, false},
		{"prompt hook", []string{"hook", "user-prompt-submit"}, "v2.2.0", nil, 0, nil, false},
		{"update", []string{"update"}, "v2.2.0", nil, 0, nil, false},
		{"version flag", []string{"--version"}, "v2.2.0", nil, 0, nil, false},
		{"completion", []string{"__complete", "st"}, "v2.2.0", nil, 0, nil, false},
		{"another process updates", []string{"status"}, "v2.2.0", nil, time.Minute, nil, false},
		{"a dead update", []string{"status"}, "v2.2.0", nil, 2 * time.Hour, nil, true},
		{"tried today", []string{"status"}, "v2.2.0", &autoUpdateResult{Target: "2.2.0", Result: "failed", At: time.Now()}, 0, nil, false},
		{"tried yesterday", []string{"status"}, "v2.2.0", &autoUpdateResult{Target: "2.2.0", Result: "failed", At: time.Now().Add(-25 * time.Hour)}, 0, nil, true},
		{"a newer target than the last try", []string{"status"}, "v2.2.1", &autoUpdateResult{Target: "2.2.0", Result: "failed", At: time.Now()}, 0, nil, true},
		{"start fails", []string{"status"}, "v2.2.0", nil, 0, os.ErrPermission, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bin := autoUpdateEnvFor(t)
			if tc.target != "" {
				cachePraxisTarget(t, tc.target)
			}
			if tc.last != nil {
				if err := writeAutoUpdate(*tc.last); err != nil {
					t.Fatal(err)
				}
			}
			dir := filepath.Join(os.Getenv("HOME"), ".praxis")
			claim := filepath.Join(dir, autoUpdateClaim)
			if tc.claimAge > 0 {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(claim, nil, 0o644); err != nil {
					t.Fatal(err)
				}
				old := time.Now().Add(-tc.claimAge)
				if err := os.Chtimes(claim, old, old); err != nil {
					t.Fatal(err)
				}
			}
			var starts []string
			startAutoUpdate = func(path, d string) error {
				if d != dir {
					t.Errorf("dir = %q, want %q", d, dir)
				}
				starts = append(starts, path)
				return tc.startErr
			}

			maybeAutoUpdate(tc.args)

			if (len(starts) == 1) != tc.want {
				t.Fatalf("starts = %v, want started %t", starts, tc.want)
			}
			if real, _ := filepath.EvalSymlinks(bin); tc.want && starts[0] != real {
				t.Errorf("started %q, want %q", starts[0], real)
			}
			_, err := os.Stat(claim)
			if tc.want && tc.startErr != nil && err == nil {
				t.Error("a failed start must release the claim")
			}
			if tc.want && tc.startErr == nil && err != nil {
				t.Error("a started update must hold the claim")
			}
		})
	}
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// fakeRelease serves 2.2.0 with build as this platform's asset.
func fakeRelease(t *testing.T, build []byte, digest string) {
	t.Helper()
	withFakeRelease(t, &selfupdate.Release{TagName: "v2.2.0", Assets: []selfupdate.Asset{{
		Name: "praxis_" + runtime.GOOS + "_" + runtime.GOARCH, BrowserDownloadURL: "https://example.test/praxis", Digest: digest,
	}}}, nil)
	orig := downloadAsset
	downloadAsset = func(_, dir string) (string, error) {
		f, err := os.CreateTemp(dir, ".praxis-update-*")
		if err != nil {
			return "", err
		}
		_, err = f.Write(build)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return f.Name(), err
	}
	t.Cleanup(func() { downloadAsset = orig })
}

func readBin(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunAutoUpdateRenamesTheVerifiedBuild(t *testing.T) {
	bin := autoUpdateEnvFor(t)
	cachePraxisTarget(t, "v2.2.0")
	build := []byte("new praxis")
	fakeRelease(t, build, "sha256:"+sha(build))
	claim := filepath.Join(os.Getenv("HOME"), ".praxis", autoUpdateClaim)
	if !claimSetup(claim, time.Now()) {
		t.Fatal("claim")
	}

	if err := runAutoUpdate(io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := readBin(t, bin); got != "new praxis" {
		t.Errorf("binary = %q", got)
	}
	r, err := readAutoUpdate()
	if err != nil || r.From != "2.1.0" || r.Target != "2.2.0" || r.Result != "upgraded" {
		t.Errorf("state = %+v, %v", r, err)
	}
	if _, err := os.Stat(claim); err == nil {
		t.Error("the claim stays after the update")
	}
	entries, _ := os.ReadDir(filepath.Dir(bin))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".praxis-update-") {
			t.Errorf("download left behind: %s", e.Name())
		}
	}
}

func TestRunAutoUpdateRefusesAnUnverifiedBuild(t *testing.T) {
	for name, digest := range map[string]string{"no SHA-256": "", "wrong SHA-256": "sha256:" + sha([]byte("other"))} {
		t.Run(name, func(t *testing.T) {
			bin := autoUpdateEnvFor(t)
			cachePraxisTarget(t, "v2.2.0")
			fakeRelease(t, []byte("new praxis"), digest)

			if err := runAutoUpdate(io.Discard); err == nil {
				t.Fatal("want an error")
			}
			if got := readBin(t, bin); got != "old praxis" {
				t.Errorf("binary = %q, want the old one", got)
			}
			if r, _ := readAutoUpdate(); r.Result != "failed" || r.Error == "" {
				t.Errorf("state = %+v", r)
			}
		})
	}
}

// The fetch can reach another release source than the daily check did.
func TestRunAutoUpdateRefusesAnOlderRelease(t *testing.T) {
	bin := autoUpdateEnvFor(t)
	cachePraxisTarget(t, "v2.2.0")
	withFakeRelease(t, &selfupdate.Release{TagName: "v2.0.2"}, nil)
	if err := runAutoUpdate(io.Discard); err == nil || !strings.Contains(err.Error(), "not newer") {
		t.Fatalf("err = %v", err)
	}
	if got := readBin(t, bin); got != "old praxis" {
		t.Errorf("binary = %q", got)
	}
}

func TestRunAutoUpdateOnANewerBinaryDoesNothing(t *testing.T) {
	bin := autoUpdateEnvFor(t)
	version = "2.2.0"
	cachePraxisTarget(t, "v2.2.0")
	withFakeRelease(t, nil, errors.New("must not fetch"))
	if err := runAutoUpdate(io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := readBin(t, bin); got != "old praxis" {
		t.Errorf("binary = %q", got)
	}
	if _, err := readAutoUpdate(); err == nil {
		t.Error("no attempt must be recorded")
	}
}

func TestRunAutoUpdateRunsBrew(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Homebrew does not run on Windows")
	}
	for name, installs := range map[string]bool{"upgraded": true, "tap without the release": false} {
		t.Run(name, func(t *testing.T) {
			autoUpdateEnvFor(t)
			bin := fakeCask(t, "2.1.0")
			selfPath = func() (string, error) { return bin, nil }
			cachePraxisTarget(t, "v2.2.0")
			var calls []string
			orig := runBrew
			runBrew = func(w io.Writer, _ string, args ...string) error {
				calls = append(calls, strings.Join(args, " "))
				if args[0] == "list" {
					// A stale 2.2.0 folder must not count: brew names the installed version.
					v := "2.1.0"
					if installs {
						v = "2.2.0"
					}
					_, err := io.WriteString(w, "Warning: Calling `postflight` is deprecated! 2.2.0\npraxis "+v+"\n")
					return err
				}
				return nil
			}
			t.Cleanup(func() { runBrew = orig })

			err := runAutoUpdate(io.Discard)

			if strings.Join(calls, "; ") != "update --quiet; upgrade --cask praxis; list --cask --versions praxis" {
				t.Errorf("brew calls = %q", calls)
			}
			r, _ := readAutoUpdate()
			if installs && (err != nil || r.Result != "upgraded") {
				t.Errorf("err = %v, state = %+v", err, r)
			}
			if !installs && (err == nil || r.Result != "failed" || !strings.Contains(r.Error, "did not install 2.2.0")) {
				t.Errorf("err = %v, state = %+v", err, r)
			}
		})
	}
}

func TestAutoUpdateNoticePrintsOnceOnTheNewVersion(t *testing.T) {
	autoUpdateEnvFor(t)
	if err := writeAutoUpdate(autoUpdateResult{From: "2.1.0", Target: "2.2.0", Result: "upgraded", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if msg := autoUpdateNotice(); msg != "" {
		t.Errorf("the old binary printed %q", msg)
	}
	version = "v2.2.0"
	if msg := autoUpdateNotice(); !strings.Contains(msg, "2.1.0 → 2.2.0") || !strings.Contains(msg, autoUpdateEnv) {
		t.Errorf("notice = %q", msg)
	}
	if msg := autoUpdateNotice(); msg != "" {
		t.Errorf("second notice = %q", msg)
	}
}

func TestAutoUpdateHandlesTheBox(t *testing.T) {
	autoUpdateEnvFor(t)
	if !autoUpdateHandles("v2.2.0") {
		t.Error("an install that updates itself shows the box")
	}
	if err := writeAutoUpdate(autoUpdateResult{Target: "2.2.0", Result: "failed", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if autoUpdateHandles("v2.2.0") {
		t.Error("a failed update must show the box")
	}
	if !autoUpdateHandles("v2.2.1") {
		t.Error("a newer target gets a new attempt")
	}
	t.Setenv(autoUpdateEnv, "1")
	if autoUpdateHandles("v2.2.1") {
		t.Error("an install that does not update itself must show the box")
	}
}

func TestAutoUpdateSnapshot(t *testing.T) {
	autoUpdateEnvFor(t)
	if a := autoUpdateSnapshot(); a.Mode != "rename" || a.OffReason != "" || a.Result != "" {
		t.Errorf("snapshot = %+v", a)
	}
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	if err := writeAutoUpdate(autoUpdateResult{Target: "2.2.0", Result: "failed", Error: "boom", At: at}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CI", "true")
	a := autoUpdateSnapshot()
	if a.Mode != "off" || a.OffReason != "ci" || a.Target != "2.2.0" || a.Error != "boom" || a.At != "2026-10-08T10:00:00Z" {
		t.Errorf("snapshot = %+v", a)
	}
}

func TestStartBackgroundUpdate(t *testing.T) {
	dir := t.TempDir()
	if err := startBackgroundUpdate(trueBinary(t), dir); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, autoUpdateLog)); err != nil {
		t.Errorf("log: %v", err)
	}
	// Windows reports a missing program as exec.ErrNotFound.
	if err := startBackgroundUpdate(filepath.Join(dir, "missing"), dir); !errors.Is(err, os.ErrNotExist) && !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("missing binary: err = %v", err)
	}
}

// A gate that closes after the start (here the opt-out) records a failed try.
func TestRunAutoUpdateStopsWhenTurnedOff(t *testing.T) {
	bin := autoUpdateEnvFor(t)
	cachePraxisTarget(t, "v2.2.0")
	t.Setenv(autoUpdateEnv, "1")
	if err := runAutoUpdate(io.Discard); err == nil || !strings.Contains(err.Error(), "off: env") {
		t.Fatalf("err = %v", err)
	}
	if got := readBin(t, bin); got != "old praxis" {
		t.Errorf("binary = %q", got)
	}
}

func TestAutoUpdatePathWithoutSelf(t *testing.T) {
	autoUpdateEnvFor(t)
	selfPath = func() (string, error) { return "", errors.New("no executable") }
	if p := autoUpdatePath(); p != "" {
		t.Errorf("path = %q", p)
	}
	selfPath = func() (string, error) { return filepath.Join(t.TempDir(), "gone"), nil }
	if p := autoUpdatePath(); p != "" {
		t.Errorf("path of a missing file = %q", p)
	}
}
