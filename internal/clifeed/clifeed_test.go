package clifeed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
)

// isolate gives the test its own HOME, with one control-plane profile in
// raptor's credentials file, and no cached feed answer.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(credentials.EnvProfile, "")
	t.Setenv("FACETS_PROFILE", "")
	t.Cleanup(credentials.SetGetwdForTest(func() (string, error) { return home, nil }))
	if err := os.MkdirAll(filepath.Join(home, ".facets"), 0o700); err != nil {
		t.Fatal(err)
	}
	creds := "[default]\ncontrol_plane_url = https://acme.console.facets.cloud\nusername = dev@acme.io\ntoken = t0k\n"
	if err := os.WriteFile(filepath.Join(home, ".facets", "credentials"), []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	ResetForTest()
	t.Cleanup(ResetForTest)
	return home
}

const feedAnswer = `{"releases": {
  "praxis": {"version": "2.1.0", "target": "2.1.0", "assets": {
    "darwin/arm64": {"url": "https://example.test/praxis_darwin_arm64", "sha256": "abc"}}},
  "raptor": {"version": "0.1.200", "target": "0.1.200", "assets": {}}}}`

func TestTargetSendsCensusOnceForBothCLIs(t *testing.T) {
	isolate(t)
	var calls atomic.Int32
	var got request
	var client string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		client = r.Header.Get("X-Facets-Client")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(feedAnswer))
	}))
	defer srv.Close()
	t.Setenv("FACETS_CLI_FEED_URL", srv.URL)

	p, err := Target("praxis")
	if err != nil || p.Target != "2.1.0" || p.Assets["darwin/arm64"].SHA256 != "abc" {
		t.Fatalf("praxis = %+v, %v", p, err)
	}
	r, err := Target("raptor")
	if err != nil || r.Target != "0.1.200" {
		t.Fatalf("raptor = %+v, %v", r, err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("feed calls = %d, want 1 for the whole process", n)
	}
	if !strings.HasPrefix(client, "praxis/") {
		t.Errorf("X-Facets-Client = %q", client)
	}
	if got.CPURL != "https://acme.console.facets.cloud" || got.User != "dev@acme.io" {
		t.Errorf("census = %+v", got)
	}
	if !installIDPattern.MatchString(got.InstallID) {
		t.Errorf("install_id = %q", got.InstallID)
	}
}

func TestTargetFailsWhenTheFeedFails(t *testing.T) {
	isolate(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	t.Setenv("FACETS_CLI_FEED_URL", srv.URL)
	if _, err := Target("praxis"); err == nil {
		t.Fatal("want an error, so the caller falls back to GitHub")
	}
}

func TestTargetFailsForAnUnknownCLI(t *testing.T) {
	isolate(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(feedAnswer))
	}))
	defer srv.Close()
	t.Setenv("FACETS_CLI_FEED_URL", srv.URL)
	if _, err := Target("skoop"); err == nil {
		t.Fatal("want an error for a CLI the feed does not name")
	}
}

func TestInstallIDIsCreatedOnceAndShared(t *testing.T) {
	home := isolate(t)
	first := InstallID()
	if !installIDPattern.MatchString(first) || InstallID() != first {
		t.Fatalf("install IDs %q / %q", first, InstallID())
	}
	info, err := os.Stat(filepath.Join(home, installIDFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("install-id file: %v, %v", info, err)
	}
	// An ID that raptor wrote first is kept as it is.
	if err := os.WriteFile(filepath.Join(home, installIDFile), []byte("from-raptor-0001\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := InstallID(); got != "from-raptor-0001" {
		t.Errorf("InstallID = %q, want raptor's", got)
	}
}
