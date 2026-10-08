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
	"github.com/Facets-cloud/praxis-cli/internal/paths"
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
	Setup     *setup `json:"setup,omitempty"`
	// Container and InstallAgeSeconds let the census tell a fresh CI container
	// (one job, a new install ID) from a real install, also when the CI system
	// sets no CI variable.
	Container         bool   `json:"container,omitempty"`
	InstallAgeSeconds *int64 `json:"install_age_s,omitempty"`
}

// containerMarkers are files that a container runtime creates.
var containerMarkers = []string{"/.dockerenv", "/run/.containerenv"}

// inContainer reports whether praxis runs in a container: a Docker or Podman
// marker file, or a Kubernetes pod.
func inContainer() bool {
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	for _, m := range containerMarkers {
		if _, err := os.Stat(m); err == nil {
			return true
		}
	}
	return false
}

// installAgeSeconds is the age of the install-ID file, which is written once:
// a few seconds on a machine that was created for this run. Nil when unknown.
func installAgeSeconds() *int64 {
	home, err := paths.Home()
	if err != nil {
		return nil
	}
	info, err := os.Stat(filepath.Join(home, installIDFile))
	if err != nil {
		return nil
	}
	age := int64(time.Since(info.ModTime()).Seconds())
	if age < 0 {
		age = 0
	}
	return &age
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

// census is what the feed records besides the identity headers: the install
// ID, the active profile, and the setup snapshot. Each part is best-effort; a
// missing one is left out.
func census() request {
	req := request{InstallID: InstallID(), Setup: collectSetup(), Container: inContainer()}
	req.InstallAgeSeconds = installAgeSeconds()
	req.CPURL, req.User = Identity()
	return req
}

// profileFlag is the --profile value of this invocation, set before any check
// runs, so the census names the profile that the command itself uses.
var profileFlag string

// SetProfileFlag records the --profile value of this invocation.
func SetProfileFlag(name string) { profileFlag = name }

// Identity is the CP URL and user that this invocation uses: the --profile
// flag, else the praxis environment variables, else the active profile. Both
// are empty when there is none.
func Identity() (cpURL, user string) {
	a, err := credentials.ResolveActive(profileFlag)
	if err != nil || !a.Loaded {
		return "", ""
	}
	return a.Profile.URL, a.Profile.Username
}

// InstallID returns this machine's install ID, and creates it on first use.
// It returns "" when the home folder cannot hold one.
func InstallID() string {
	home, err := paths.Home()
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
