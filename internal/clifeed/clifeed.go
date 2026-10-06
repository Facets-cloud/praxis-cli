// Package clifeed asks the central CLI feed (cross-control-plane,
// POST /cli/v1/check) for the target release of praxis and raptor, and sends
// the census with the same call: the identity headers, this machine's install
// ID, and the CP URL and user of the active profile.
//
// The feed replaces the GitHub API as the first source of the daily update
// check. Callers fall back to GitHub when it fails.
package clifeed

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/httpclient"
)

// defaultURL is the feed. FACETS_CLI_FEED_URL overrides it for tests.
const defaultURL = "https://cross.facetsapp.cloud/cli/v1/check"

// timeout is short, so a slow feed leaves time for the GitHub fallback.
const timeout = 3 * time.Second

// installIDFile names this machine for the census. raptor reads and writes
// the same file, so both CLIs on one machine report one install.
const installIDFile = ".facets/install-id"

var installIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// Release is one CLI's entry in the feed answer. Target is the version the
// CLI must move to.
type Release struct {
	Version string           `json:"version"`
	Target  string           `json:"target"`
	Assets  map[string]Asset `json:"assets"` // keyed by "<os>/<arch>"
}

// Asset is one download. SHA256 is lowercase hex, or empty when unknown.
type Asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type response struct {
	Releases map[string]Release `json:"releases"`
}

type request struct {
	InstallID string `json:"install_id,omitempty"`
	CPURL     string `json:"cp_url,omitempty"`
	User      string `json:"user,omitempty"`
}

var (
	mu      sync.Mutex
	done    bool
	answer  response
	lastErr error
)

// Target returns the feed's release for cli ("praxis" or "raptor"). The feed
// is called at most once in a process, so the praxis and raptor checks of
// one run share one call and one census record.
func Target(cli string) (Release, error) {
	mu.Lock()
	defer mu.Unlock()
	if !done {
		answer, lastErr = check()
		done = true
	}
	if lastErr != nil {
		return Release{}, lastErr
	}
	r, ok := answer.Releases[cli]
	if !ok || r.Target == "" {
		return Release{}, fmt.Errorf("the feed has no %s release", cli)
	}
	return r, nil
}

// ResetForTest forgets the answer of this process, so a test can call the
// feed again.
func ResetForTest() {
	mu.Lock()
	defer mu.Unlock()
	done, answer, lastErr = false, response{}, nil
}

func endpoint() string {
	if u := os.Getenv("FACETS_CLI_FEED_URL"); u != "" {
		return u
	}
	return defaultURL
}

func check() (response, error) {
	body, err := json.Marshal(census())
	if err != nil {
		return response{}, err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint(), bytes.NewReader(body))
	if err != nil {
		return response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpclient.New(timeout).Do(req)
	if err != nil {
		return response{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return response{}, fmt.Errorf("feed returned %s", resp.Status)
	}
	var out response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return response{}, err
	}
	if len(out.Releases) == 0 {
		return response{}, errors.New("the feed returned no releases")
	}
	return out, nil
}

// census is what the feed records besides the identity headers. Each part is
// best-effort; a missing one is left out.
func census() request {
	req := request{InstallID: InstallID()}
	if a, err := credentials.ResolveActive(""); err == nil && a.Loaded {
		req.CPURL, req.User = a.Profile.URL, a.Profile.Username
	}
	return req
}

// InstallID returns this machine's install ID, and creates it on first use.
// It returns "" when the home folder cannot hold one.
func InstallID() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, installIDFile)
	if id := readInstallID(path); id != "" {
		return id
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	h := hex.EncodeToString(b[:])
	id := h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ""
	}
	// O_EXCL: when raptor creates the file at the same moment, keep its ID.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return readInstallID(path)
	}
	defer f.Close()
	if _, err := f.WriteString(id + "\n"); err != nil {
		return ""
	}
	return id
}

func readInstallID(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(data))
	if !installIDPattern.MatchString(id) {
		return ""
	}
	return id
}
