package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/raptorinstall"
	"github.com/Facets-cloud/praxis-cli/internal/selfupdate"
)

// useRealRaptorSetup undoes TestMain's no-op stubs for one test.
func useRealRaptorSetup(t *testing.T) {
	t.Helper()
	oldEnsure, oldUpdate := ensureRaptorBinary, updateRaptor
	ensureRaptorBinary, updateRaptor = raptorinstall.Ensure, runRaptorUpgrade
	t.Cleanup(func() { ensureRaptorBinary, updateRaptor = oldEnsure, oldUpdate })
}

// fakeRaptor puts a raptor on PATH that reports version and records the
// arguments of its upgrade call in the returned marker file.
func fakeRaptor(t *testing.T, home, version string, upgradeExit int) (marker string) {
	t.Helper()
	bin := filepath.Join(home, "bin", "raptor")
	mustMkdir(t, filepath.Dir(bin))
	marker = filepath.Join(home, "upgrade-args")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'raptor version %s'; exit 0; fi\n"+
		"printf '%%s' \"$*\" > '%s'\necho 'upgrade output'\nexit %d\n", version, marker, upgradeExit)
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(bin))
	return marker
}

func TestUpdateAlsoUpgradesRaptor(t *testing.T) {
	tests := []struct {
		name        string
		tag         string
		brew        bool
		raptorExit  int
		wantUpdated bool
		wantReason  string
		wantPraxis  string
	}{
		{"praxis current", "v" + version, false, 0, false, "already_latest", "old-praxis"},
		{"praxis updated", "v999.0.0", false, 0, true, "", "new-praxis"},
		{"homebrew praxis", "v999.0.0", true, 0, false, "homebrew_managed", "old-praxis"},
		{"raptor upgrade fails", "v999.0.0", false, 6, true, "", "new-praxis"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useRealRaptorSetup(t)
			home := t.TempDir()
			t.Setenv("HOME", home)
			marker := fakeRaptor(t, home, "0.1.100", tc.raptorExit)

			self := filepath.Join(home, "praxis")
			if tc.brew {
				self = filepath.Join(home, "Caskroom", "praxis", "1", "praxis")
			}
			mustMkdir(t, filepath.Dir(self))
			if err := os.WriteFile(self, []byte("old-praxis"), 0700); err != nil {
				t.Fatal(err)
			}
			withSelfPath(t, self)
			withFakeRelease(t, &selfupdate.Release{TagName: tc.tag, Assets: []selfupdate.Asset{{Name: "praxis_" + runtime.GOOS + "_" + runtime.GOARCH}}}, nil)
			origDownload := downloadAsset
			downloadAsset = func(string) (string, error) {
				p := filepath.Join(home, "new-praxis")
				return p, os.WriteFile(p, []byte("new-praxis"), 0700)
			}
			t.Cleanup(func() { downloadAsset = origDownload; updateCmd.SetOut(nil) })

			var out bytes.Buffer
			updateCmd.SetOut(&out)
			err := updateCmd.RunE(updateCmd, nil)
			if (err != nil) != (tc.raptorExit != 0) {
				t.Fatalf("err = %v, output = %s", err, out.String())
			}
			var payload struct {
				Updated bool                `json:"updated"`
				Reason  string              `json:"reason"`
				Raptor  raptorUpgradeResult `json:"raptor"`
			}
			if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
				t.Fatalf("raptor output corrupted the JSON: %s", out.String())
			}
			if payload.Updated != tc.wantUpdated || payload.Reason != tc.wantReason {
				t.Errorf("praxis outcome = %+v", payload)
			}
			if !payload.Raptor.Attempted || payload.Raptor.Completed != (tc.raptorExit == 0) {
				t.Errorf("raptor outcome = %+v", payload.Raptor)
			}
			if args, _ := os.ReadFile(marker); string(args) != "upgrade --yes" {
				t.Errorf("raptor ran with %q, want \"upgrade --yes\"", args)
			}
			if got, _ := os.ReadFile(self); string(got) != tc.wantPraxis {
				t.Errorf("praxis binary = %q, want %q", got, tc.wantPraxis)
			}
		})
	}
}

func TestRunRaptorUpgrade(t *testing.T) {
	t.Run("interactive streams raptor and passes no --yes", func(t *testing.T) {
		useRealRaptorSetup(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		marker := fakeRaptor(t, home, "0.1.100", 0)
		var out bytes.Buffer
		res, err := runRaptorUpgrade(&out, false, false)
		if err != nil || !res.Completed || !strings.Contains(out.String(), "upgrade output") {
			t.Fatalf("res = %+v, err = %v, out = %s", res, err, out.String())
		}
		if args, _ := os.ReadFile(marker); string(args) != "upgrade" {
			t.Errorf("raptor ran with %q", args)
		}
	})
	t.Run("development build is left alone", func(t *testing.T) {
		useRealRaptorSetup(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		marker := fakeRaptor(t, home, "0.development", 0)
		res, err := runRaptorUpgrade(io.Discard, true, true)
		if err != nil || res.Attempted || !strings.Contains(res.Warning, "development build") {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Error("raptor upgrade ran on a development build")
		}
	})
	t.Run("install failure is reported, nothing runs", func(t *testing.T) {
		useRealRaptorSetup(t)
		ensureRaptorBinary = func() (raptorinstall.Result, error) {
			return raptorinstall.Result{}, errors.New("download unavailable")
		}
		res, err := runRaptorUpgrade(io.Discard, true, true)
		if err == nil || res.Attempted || !strings.Contains(res.Warning, "download unavailable") {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
	})
}

type raptorReleaseTransport func(*http.Request) (*http.Response, error)

func (f raptorReleaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Login installs a missing raptor from the public release, verified by digest,
// and never sends the control-plane token to GitHub.
func TestLoginInstallsMissingRaptor(t *testing.T) {
	useRealRaptorSetup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	stubMCPManifestFetch(t)
	binary := "#!/bin/sh\nexit 0\n"
	requests := 0
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })
	http.DefaultTransport = raptorReleaseTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("Authorization") != "" {
			t.Error("control-plane credentials sent to a public release host")
		}
		var body string
		switch {
		case r.URL.Host == "api.github.com" && strings.HasSuffix(r.URL.Path, "/Facets-cloud/raptor-releases/releases/latest"):
			body = fmt.Sprintf(`{"assets":[{"name":%q,"size":%d,"digest":"sha256:%x","browser_download_url":"https://github.com/Facets-cloud/raptor-releases/releases/download/v1.2.3/bin"}]}`,
				raptorAssetName(runtime.GOOS, runtime.GOARCH), len(binary), sha256.Sum256([]byte(binary)))
		case r.URL.Host == "github.com":
			body = binary
		default:
			t.Errorf("unexpected request %s", r.URL)
			return nil, errors.New("unexpected request")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})

	state := runPostAuthSetup(io.Discard, true, "https://cp.invalid", bearer("private-pat"))
	path := filepath.Join(home, ".local", "bin", "raptor")
	if got, err := os.ReadFile(path); err != nil || string(got) != binary {
		t.Fatalf("raptor not installed: %q, %v", got, err)
	}
	if info, _ := os.Stat(path); info.Mode()&0111 == 0 {
		t.Error("installed raptor is not executable")
	}
	if requests != 2 {
		t.Errorf("requests = %d, want release metadata + binary", requests)
	}
	got, ok := setupPayload("p", "u", "https://cp.invalid", "", false, state)["raptor_binary"].(raptorinstall.Result)
	if !ok || !got.Installed || got.Path != path {
		t.Errorf("raptor_binary = %+v", got)
	}
}

func TestLoginContinuesWhenRaptorInstallFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stubMCPManifestFetch(t)
	orig := ensureRaptorBinary
	ensureRaptorBinary = func() (raptorinstall.Result, error) {
		return raptorinstall.Result{}, errors.New("checksum rejected")
	}
	t.Cleanup(func() { ensureRaptorBinary = orig })

	state := runPostAuthSetup(io.Discard, true, "https://cp.invalid", bearer("tok"))
	if !strings.Contains(state.raptorWarning, "checksum rejected") {
		t.Errorf("raptorWarning = %q", state.raptorWarning)
	}
	if state.snapshotPath == "" {
		t.Error("a raptor failure stopped the rest of login setup")
	}
}

func TestSetupInstallsRaptorButFirstRunDoesNot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	calls := 0
	orig := ensureRaptorBinary
	ensureRaptorBinary = func() (raptorinstall.Result, error) {
		calls++
		return raptorinstall.Result{}, errors.New("offline")
	}
	t.Cleanup(func() { ensureRaptorBinary = orig; setupCmd.SetOut(nil) })

	maybeFirstRunBootstrap([]string{"status"})
	if calls != 0 {
		t.Fatalf("first run tried to install raptor %d time(s); it must stay offline", calls)
	}

	var out bytes.Buffer
	setupCmd.SetOut(&out)
	if err := setupCmd.RunE(setupCmd, nil); err != nil {
		t.Fatalf("a raptor failure must not fail setup: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("setup JSON: %v\n%s", err, out.String())
	}
	if calls != 1 || payload["raptor_warning"] != "offline" {
		t.Errorf("calls = %d, payload = %v", calls, payload)
	}
}
