package cmd

import (
	"bytes"
	"errors"
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
