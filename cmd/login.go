package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/httpclient"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/raptorstate"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

// pollInterval is how often the CLI hits GET /v1/cli-session/<nonce>/key
// while waiting for the browser modal to deposit a key. 1.5s is fast
// enough to feel responsive without spamming the server; well under the
// 5-minute server-side TTL.
const pollInterval = 1500 * time.Millisecond

// pollRequestTimeout bounds a SINGLE poll attempt. A request that
// outlives it is a transient failure (retry), never the overall login
// deadline. Var rather than const so tests can shrink it.
var pollRequestTimeout = 5 * time.Second

var (
	loginURL           string
	loginToken         string
	loginForce         bool
	loginLocal         bool
	loginJSON          bool
	loginTimeout       time.Duration
	loginRaptorProfile string
	loginDryRun        bool
)

// browserLoginFn and postAuthSetup are package-level seams so tests can
// exercise login's path selection (reuse vs. browser) and persistence
// without opening a browser, hitting the network, or installing skills.
var (
	browserLoginFn   = browserSessionPollLogin
	interactivePATFn = tryInteractivePAT
	postAuthSetup    = runPostAuthSetup
)

// osExit is a seam over os.Exit so tests can exercise the fatal-exit paths
// (which must terminate with a specific exit code, not bubble an error up
// to Execute and exit 1) without killing the test binary.
var osExit = os.Exit

func init() {
	// --profile is the global flag on rootCmd (see root.go): for login it names
	// the profile to create or update. One flag, one variable, both positions.
	loginCmd.Flags().StringVar(&loginURL, "url", "", "Praxis deployment URL (a new profile needs one: pass it here, or login asks for it on a terminal; existing profiles reuse their saved URL)")
	loginCmd.Flags().StringVar(&loginToken, "token", "", "skip browser flow; save and verify the given API key directly")
	loginCmd.Flags().BoolVar(&loginForce, "force", false, "skip the stored token and re-authenticate from the start of the chain")
	loginCmd.Flags().BoolVar(&loginLocal, "local", false,
		"pin this profile to the current directory tree (writes <cwd>/.praxis, and <cwd>/.facets/credentials for raptor) and install its skills project-scoped, instead of switching the global profile")
	loginCmd.Flags().BoolVar(&loginJSON, "json", false, "JSON output")
	loginCmd.Flags().DurationVar(&loginTimeout, "timeout", 90*time.Second, "max time to wait for browser callback")
	// No backticks in this usage string: pflag's UnquoteUsage treats a
	// backticked phrase as the flag's value placeholder, which mangled the
	// help table into `--raptor-profile praxis status`.
	loginCmd.Flags().StringVar(&loginRaptorProfile, "raptor-profile", "",
		"pair this praxis profile with a raptor profile (a ~/.facets/credentials section); login authenticates with that section's control-plane token, 'praxis status' reports raptor via it, and AI hosts prefix raptor commands with FACETS_PROFILE=<name>")
	loginCmd.Flags().BoolVar(&loginDryRun, "dry-run", false,
		"report what login would do (profile, URL reachability, browser-or-reuse, skill effect) and exit — no browser, no API key, no credential or skill changes")
	rootCmd.AddCommand(loginCmd)
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate, install meta-skill, and sync this profile's org catalog",
	Long: `Single entry point for setup. Login does, in order:

  1. Install the praxis meta-skill into every detected AI host
     (~/.claude/skills/praxis, plus ~/.agents/skills/praxis — the
      shared alias Codex and Gemini CLI both read) — idempotent.
  2. Authenticate, preferring a control-plane PAT at every step:
       a. the active profile's stored token, if still valid for this URL
          (no browser)
       b. the control-plane PAT in raptor's ~/.facets/credentials
          (no browser)
       c. the control plane's personal-access-token page — the same page
          "raptor login" opens; create a token, paste it back
       d. a Praxis API key, created in the browser
     Login walks the chain until one works. Use --token to supply a
     Praxis API key directly, or --force to skip (a) and
     re-authenticate.
  3. Save credentials and flip the active profile pointer. A control-plane
     PAT is also saved as a raptor profile in ~/.facets/credentials (the
     section named after this profile), so raptor needs no second login.
  4. Wipe any praxis-* org skills from the previous profile.
  5. Fetch this profile's skill catalog from the server and install
     each entry as praxis-<name> across all detected AI hosts.
  6. Refresh ~/.praxis/mcp-tools.json from the server's MCP manifest.

Multiple deployments? Use --profile to keep them separate:

  praxis login --url https://acme.console.facets.cloud → "default"
  praxis login --profile acme --url https://...   → "acme"
  praxis login --profile bigcorp --url https://.. → "bigcorp"

Use login to CREATE a profile or re-authenticate one whose token has
expired. To switch between profiles that are already authenticated,
` + "`praxis profiles use <name>`" + ` is the shorter path — same skill +
manifest re-sync, no browser, and it refuses without changing anything
if the stored token turns out to be dead.

Re-running login (with the same profile or a different one) also
refreshes skills + manifest snapshot; ` + "`praxis refresh-skills`" + ` does
it for the active profile without touching credentials.

Not sure what a login invocation will do? Add --dry-run: it reports the
resolved profile and URL, whether the server is reachable, whether the
browser would open or a stored token be reused, and what would happen to
installed skills — then exits without changing anything.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(loginJSON, false, out)

		// Honor an explicit selection (-p, else $PRAXIS_PROFILE) so a session
		// scoped to one profile logs into that one. With neither, login targets
		// "default": it deliberately does NOT fall back to the active profile,
		// so it can't silently re-auth another profile just because a project
		// pointer names one.
		profileName, _ := explicitProfile()
		if profileName == "" {
			profileName = credentials.DefaultProfileName
		}

		baseURL, err := resolveLoginURL(profileName, loginURL)
		if err != nil {
			// Same as `raptor login`: on a terminal, ask for the URL.
			if typed := promptLoginURL(asJSON); typed != "" {
				baseURL, err = typed, nil
			}
		}
		if err != nil {
			render.PrintError(out, asJSON, err.Error(),
				"pass --url https://<account-id>.console.facets.cloud to create this profile",
				exitcode.Usage)
			return err
		}

		// --dry-run: report the plan and exit before ANY side effect —
		// no browser, no key minted, no credential write, no skill churn.
		if loginDryRun {
			return runLoginDryRun(out, asJSON, profileName, baseURL, loginLocal)
		}

		// --token is the explicit non-browser path: verify the supplied
		// key and persist it, unchanged by the reuse logic.
		if loginToken != "" {
			return saveAndVerifyToken(out, asJSON, profileName, baseURL, loginToken, loginLocal)
		}

		// Smart default: if the active profile already has a token valid
		// for this URL, refresh skills + manifest without a browser hop.
		// --force skips only this step and re-enters the chain below it.
		if !loginForce {
			handled, rerr := tryReuseStoredToken(out, asJSON, profileName, baseURL, loginLocal)
			if handled {
				return rerr
			}
			// Nothing to reuse: a control-plane PAT already on this machine
			// is the best credential — no browser at all.
			if handled, ferr := tryFacetsPAT(out, asJSON, profileName, baseURL, loginLocal); handled {
				return ferr
			}
		}
		// A control-plane PAT beats a Praxis API key even when raptor left none.
		if handled, perr := interactivePATFn(out, asJSON, profileName, baseURL, loginLocal); handled {
			return perr
		}
		return browserLoginFn(out, asJSON, profileName, baseURL, loginTimeout, loginLocal)
	},
}

// resolveLoginURL resolves the URL for a NEW or EXISTING profile during
// login: explicit --url > existing profile's saved URL > the control plane
// raptor is logged in to. There is no built-in default: with none of the three
// the CLI cannot safely infer which organization deployment the user means.
//
// Raptor's control plane is not a guess — it's this machine's actual state, and
// in facets mode the agent server is served under that same host, so it is the
// deployment a raptor user means by a bare `praxis login`. A NAMED profile only
// inherits the raptor section it is paired with (the pin, else its own name):
// `praxis login -p acme` is a second tenant, not raptor's default one.
func resolveLoginURL(profileName, flagURL string) (string, error) {
	if flagURL != "" {
		return normalizeBaseURL(flagURL), nil
	}
	store, _ := credentials.Load()
	if p, ok := store[profileName]; ok && p.URL != "" {
		return normalizeBaseURL(p.URL), nil
	}
	pin := raptorPin(profileName)
	if pin == "" && profileName != credentials.DefaultProfileName {
		pin = profileName
	}
	if st := raptorstate.Resolve(pin); st.Found && st.ControlPlaneURL != "" {
		return normalizeBaseURL(st.ControlPlaneURL), nil
	}
	return "", fmt.Errorf("profile %q has no saved URL; pass --url to create it (or run `raptor login` first)", profileName)
}

// normalizeBaseURL strips trailing slashes so path concatenation
// (baseURL + "/v1/...") never produces a double slash, regardless of
// how the user typed --url or what an older CLI persisted.
func normalizeBaseURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// tryReuseStoredToken attempts a no-browser login using the token already
// stored for profileName.
//
// It returns handled=true when it has TAKEN OWNERSHIP of the login and the
// caller must NOT fall back to the browser. That covers two cases:
//   - success: the stored token verified and the persist+setup tail ran
//     (the returned error is that tail's result, nil on success).
//   - hard stop: the server was unreachable, so the token could be neither
//     confirmed nor refuted. We refuse to clobber a possibly-valid token
//     with a browser re-login over a transient blip, so it prints a
//     diagnostic and exits via osExit(exitcode.Network). (It still returns
//     handled=true for the stubbed-osExit test path.)
//
// handled=false means no reuse was possible and the caller should proceed to
// the browser flow — no stored token, the profile is being re-targeted at a
// different URL, or the server actively REJECTED the token (expired/revoked,
// HTTP 401/403). The error is always nil here. A rejected token is reported
// as a one-line notice on stderr, not an error, so the fallback is smooth.
func tryReuseStoredToken(out io.Writer, asJSON bool, profileName, baseURL string, local bool) (bool, error) {
	store, err := credentials.Load()
	if err != nil {
		return false, nil // can't read the store — just use the browser
	}
	prof, ok := store[profileName]
	if !ok || prof.Token == "" {
		return false, nil // nothing stored to reuse
	}
	if prof.URL != baseURL {
		// Re-targeting this profile at a different URL: the stored token
		// belongs to the old deployment, so it can't be reused here.
		return false, nil
	}

	user, err := fetchAuthMe(baseURL, prof.Auth())
	if err != nil {
		if errors.Is(err, errTokenRejected) {
			// The server gave a verdict: this token is dead. Falling back to
			// the browser to mint a fresh one is exactly right.
			if !asJSON {
				fmt.Fprintf(os.Stderr,
					"Stored token for profile %q is no longer valid (%v); opening browser…\n",
					profileName, err)
			}
			return false, nil // graceful fallback to the browser
		}
		// Transient: timeout, connection refused, 5xx — the token's validity
		// is unknown. Do NOT mislabel it "no longer valid" or force a browser
		// re-login over a flaky network. Leave the stored token untouched and
		// abort with the network exit code so this is just a retry.
		render.PrintError(out, asJSON,
			fmt.Sprintf("couldn't reach %s to verify the stored token for profile %q: %v", baseURL, profileName, err),
			"the server was unreachable — your stored token was left intact; check your connection and re-run `praxis login`",
			exitcode.Network)
		osExit(exitcode.Network)
		// Reached only under test (osExit stubbed). Report handled=true so the
		// caller never falls through to the browser, mirroring production where
		// the process has already exited.
		return true, err
	}
	// Reuse the stored profile as-is otherwise — notably its Username/AuthMode,
	// so a facets profile's identity header keeps working across reuse.
	return true, persistAndSetup(out, asJSON, profileName, applyCanonical(prof, user), user.Email, local)
}

// browserSessionPollLogin opens the browser to the api-keys page with a
// cli_session nonce, then polls the server-side session endpoint until
// the modal deposits the freshly-created key (or timeout elapses).
//
// This replaces the earlier http://127.0.0.1:<port>/key listener design,
// which was increasingly blocked by browser security policies (Brave
// Shields' localhost protection, Chromium Private Network Access). The
// browser → server hop is now strictly same-origin, so neither CORS nor
// PNA nor Shields are involved.
func browserSessionPollLogin(out io.Writer, asJSON bool, profileName, baseURL string, timeout time.Duration, local bool) error {
	sessionNonce := randomNonce()

	openURL, err := buildLoginURL(baseURL, sessionNonce, suggestedKeyName())
	if err != nil {
		render.PrintError(out, asJSON, err.Error(),
			"check the --url value — it must be a valid URL",
			exitcode.Usage)
		os.Exit(exitcode.Usage)
	}
	fmt.Fprintln(os.Stderr, "Opening browser to create a Praxis API key…")
	fmt.Fprintln(os.Stderr, "  ", openURL)
	fmt.Fprintf(os.Stderr, "Waiting for the key (timeout %s)…\n", timeout)
	if err := openBrowser(openURL); err != nil {
		fmt.Fprintf(os.Stderr, "\nCouldn't auto-open browser (%v). Open the URL above manually.\n", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	key, err := pollSessionKey(ctx, baseURL, sessionNonce, pollInterval)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			render.PrintError(out, asJSON, "login timed out",
				"finish the API key creation in the browser within the timeout", exitcode.Auth)
			os.Exit(exitcode.Auth)
		}
		render.PrintError(out, asJSON, err.Error(),
			"the login handshake failed", exitcode.Auth)
		os.Exit(exitcode.Auth)
	}
	return saveAndVerifyToken(out, asJSON, profileName, baseURL, key, local)
}

// pollSessionKey polls GET {baseURL}/ai-api/v1/cli-session/{nonce}/key
// at the given interval until one of:
//
//   - 200 OK with {plaintext_key: "..."} — returns the key.
//   - The context deadline fires — returns context.DeadlineExceeded.
//   - The server returns 400 or 404 — returns a fatal error (the nonce
//     was malformed or never created; retry would never succeed).
//
// 204 (pending), 5xx, and transient network errors all keep polling
// silently. The CLI side is the source of truth for the polling loop;
// the server is intentionally simple and stateless-ish.
//
// `interval` is the gap between attempts. Splitting it out as a
// parameter keeps the function trivially testable without sub-second
// fakery — tests pass 10–50ms intervals.
func pollSessionKey(ctx context.Context, baseURL, nonce string, interval time.Duration) (string, error) {
	endpoint := fmt.Sprintf("%s/ai-api/v1/cli-session/%s/key",
		strings.TrimRight(baseURL, "/"), nonce)
	client := httpclient.New(pollRequestTimeout)

	for {
		key, status, err := pollSessionOnce(ctx, client, endpoint)
		if err != nil {
			return "", err
		}
		if status == pollReady {
			return key, nil
		}
		// pending or transient — wait then retry.
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
	}
}

type pollStatus int

const (
	pollPending pollStatus = iota
	pollReady
	pollTransient
)

// pollSessionOnce does a single GET and classifies the result. It
// returns a fatal err only when retrying would never help (malformed
// nonce, corrupt response). 5xx and network errors are folded into
// pollTransient so the caller's loop just keeps going.
func pollSessionOnce(ctx context.Context, client *http.Client, endpoint string) (string, pollStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", pollPending, err
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			// The OVERALL login deadline (or a cancel) fired — stop.
			return "", pollPending, ctx.Err()
		}
		// Everything else is transient and must keep the loop polling.
		// That includes the client's own per-request timeout, whose
		// error matches errors.Is(err, context.DeadlineExceeded) since
		// Go 1.16 — checking the error instead of ctx.Err() here used
		// to abort the whole login as "timed out" after one slow poll.
		return "", pollTransient, nil
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return "", pollPending, nil
	case http.StatusOK:
		var body struct {
			PlaintextKey string `json:"plaintext_key"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return "", pollPending, fmt.Errorf("decode session response: %w", err)
		}
		if body.PlaintextKey == "" {
			return "", pollPending, fmt.Errorf("server returned empty plaintext_key")
		}
		return body.PlaintextKey, pollReady, nil
	case http.StatusBadRequest, http.StatusNotFound:
		return "", pollPending, fmt.Errorf("server rejected nonce: HTTP %d", resp.StatusCode)
	default:
		// 5xx or unexpected 2xx — keep trying.
		return "", pollTransient, nil
	}
}

func buildLoginURL(baseURL, sessionNonce, suggestedName string) (string, error) {
	u, err := url.Parse(baseURL + "/ui/ai/settings/api-keys")
	if err != nil {
		return "", fmt.Errorf("invalid login URL %q: %w", baseURL, err)
	}
	q := u.Query()
	q.Set("cli_session", sessionNonce)
	q.Set("suggested_name", suggestedName)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// suggestedKeyName produces a unique-per-invocation key name so a
// developer re-running `praxis login` doesn't hit the modal's
// "name already exists" validation. 5 hex chars = 20 bits of
// randomness, plenty to avoid collisions across a single user's keys.
// The output matches the modal's name pattern ^[a-z0-9_-]+$.
func suggestedKeyName() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return "praxis-cli-" + hex.EncodeToString(b)[:5]
}

// tryFacetsPAT attempts a no-browser login with the control-plane PAT already
// in raptor's ~/.facets/credentials, sent as Bearer plus an X-Facets-Username
// identity header.
//
// Like tryReuseStoredToken, handled=false means the caller should fall through
// to the browser — no usable raptor profile, or the server rejected the PAT.
// The fallback uses the caller's baseURL, never the candidate's.
func tryFacetsPAT(out io.Writer, asJSON bool, profileName, baseURL string, local bool) (bool, error) {
	c, ok := facetsPATCandidate(profileName, baseURL)
	if !ok {
		return false, nil
	}

	prof := c.asProfile()
	user, err := fetchAuthMe(c.url, prof.Auth())
	if err != nil {
		// Only a server verdict means "this PAT is dead". A timeout or 5xx
		// leaves it unjudged, so say so rather than blaming the credential.
		// Stderr in both output modes, like resolveRaptorPairing's warnings:
		// it can't corrupt --json, and a silent fallback to the browser is
		// the one thing an AI host can't diagnose.
		verdict := "could not be verified"
		if errors.Is(err, errTokenRejected) {
			verdict = "was not accepted"
		}
		fmt.Fprintf(os.Stderr,
			"Control-plane PAT for %s (~/.facets/credentials profile %q) %s at %s (%v); opening browser…\n",
			c.username, c.section, verdict, c.url, err)
		return false, nil
	}
	return true, persistVerified(out, asJSON, profileName, prof, user, c.username, local)
}

// persistVerified is the tail both PAT paths share: canonicalize the URL, then
// name the login after the server's email, falling back to the username that
// authenticated when the server doesn't return one.
func persistVerified(out io.Writer, asJSON bool, profileName string, prof credentials.Profile,
	user *authMeResponse, fallbackName string, local bool) error {
	display := user.Email
	if display == "" {
		display = fallbackName
	}
	return persistAndSetup(out, asJSON, profileName, applyCanonical(prof, user), display, local)
}

// facetsPAT is a control-plane credential a login can try: the
// ~/.facets/credentials section it came from, the pair, and where to send it.
type facetsPAT struct {
	section  string
	username string
	token    string
	url      string
}

// asProfile is the praxis profile this credential would be saved and
// authenticated as.
func (c facetsPAT) asProfile() credentials.Profile {
	return credentials.FacetsProfile(c.url, c.username, c.token)
}

// facetsPATCandidate returns the control-plane PAT praxis would authenticate
// this login with, or ok=false when there is none. Shared by the login path and
// --dry-run so the report can't disagree with what login does. The section is
// chosen by raptor's own rules (pin > FACETS_PROFILE > [default] > sole), so
// praxis picks the PAT raptor commands in the same shell would use.
//
// A candidate is only produced for an https URL that IS that control plane: in
// facets mode the agent server is served under the control-plane host itself (it
// derives its CP URL from its own X-Forwarded-Host), so a host mismatch could
// never validate — and a control-plane PAT must never be offered to a host that
// was merely typed after --url. Loopback is exempt: a developer's own agent
// server running against a remote CP.
func facetsPATCandidate(profileName, baseURL string) (facetsPAT, bool) {
	st := raptorstate.Resolve(raptorPin(profileName))
	if !st.Found {
		return facetsPAT{}, false
	}
	// A stored PAT additionally may only travel to the control plane it came
	// from — never to a host merely typed after --url.
	if !patTransportOK(baseURL) ||
		(!isLoopbackURL(baseURL) && !raptorstate.MatchesHost(baseURL, st.ControlPlaneURL)) {
		return facetsPAT{}, false
	}
	username, token, ok := raptorstate.PAT(st.Profile)
	if !ok {
		return facetsPAT{}, false
	}
	return facetsPAT{section: st.Profile, username: username, token: token, url: baseURL}, true
}

// patTransportOK reports whether a control-plane PAT may be sent to this URL at
// all. A PAT never travels in cleartext; loopback is exempt because that is a
// developer's own agent server, with no network to eavesdrop. Shared by every
// PAT path (stored, pasted, and the --dry-run report) so a trust-boundary rule
// has exactly one expression.
func patTransportOK(baseURL string) bool {
	return strings.HasPrefix(baseURL, "https://") || isLoopbackURL(baseURL)
}

// isLoopbackURL reports whether a URL points at this machine.
func isLoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	h := u.Hostname()
	return h == "localhost" || net.ParseIP(h).IsLoopback()
}

// saveAndVerifyToken verifies a freshly-obtained token (from --token or
// the browser flow) and persists it. A verification failure here is fatal
// — the user explicitly supplied this key, so there's no graceful
// fallback to attempt.
func saveAndVerifyToken(out io.Writer, asJSON bool, profileName, baseURL, token string, local bool) error {
	// --token / browser flow always yields a Praxis API key → Bearer.
	// Route through Auth() so "Bearer " is built in exactly one place.
	user, err := fetchAuthMe(baseURL, credentials.Profile{Token: token}.Auth())
	if err != nil {
		render.PrintError(out, asJSON,
			fmt.Sprintf("token validation failed: %v", err),
			"the API key may be invalid, revoked, or the URL is wrong",
			exitcode.Auth)
		os.Exit(exitcode.Auth)
	}
	// Store the canonical (post-redirect) host, not what the user typed:
	// a stored apex URL would force every later MCP invoke through the
	// apex → www 301 (issue #19-A).
	if user.canonicalBaseURL != "" {
		baseURL = user.canonicalBaseURL
	}
	prof := credentials.Profile{URL: baseURL, Username: user.Email, Token: token}
	return persistAndSetup(out, asJSON, profileName, prof, user.Email, local)
}

// persistAndSetup saves the verified token under profileName, sets the
// active-profile pointer (global, or project-local when `local` is set),
// runs post-auth setup (meta-skill + catalog + MCP manifest), and renders
// the result. It is the shared tail of both the verify-then-save path
// (saveAndVerifyToken) and the reuse path (tryReuseStoredToken). Returning an
// error rather than os.Exit lets the reuse path stay non-fatal up to this
// point.
//
// Credentials are ALWAYS saved globally (~/.praxis/credentials). What `local`
// changes is the active-profile pointer and the install scope:
//   - global (default): flip ~/.praxis/config.json and install user-level,
//     pinning the active root to home so being inside a project tree doesn't
//     accidentally scope the install.
//   - local: write <cwd>/.praxis/config.json and install project-scoped,
//     leaving the global pointer untouched.
//
// persistAndSetup takes the fully-built profile to save (its URL/Username/
// Token/AuthMode are authoritative — e.g. a facets profile keeps its
// control-plane username so Auth() can rebuild the X-Facets-Username header
// on reuse) and a displayName used only for the human/JSON "logged in as" line.
func persistAndSetup(out io.Writer, asJSON bool, profileName string, prof credentials.Profile, displayName string, local bool) error {
	baseURL := prof.URL
	prof.RaptorProfile = resolveRaptorPairing(profileName, baseURL)
	if prof.AuthMode == credentials.AuthModeBasic {
		// The PAT becomes a raptor profile too; a non-default name is pinned
		// so `praxis status` and the AI host select that section.
		if s := raptorSection(profileName, prof.RaptorProfile); s != credentials.DefaultProfileName {
			prof.RaptorProfile = s
		}
	}
	if err := credentials.Put(profileName, prof); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}

	projectRoot, restore, err := activateProfile(profileName, local)
	if err != nil {
		return err
	}
	defer restore()

	raptorW, raptorWarn := writeRaptorCredentials(profileName, prof, projectRoot)

	// Post-auth: install meta-skill, wipe previous org skills, install
	// this profile's catalog, refresh the MCP tools snapshot. The HTTP
	// calls use the profile's full auth headers (Bearer + X-Facets-Username).
	state := postAuthSetup(out, asJSON, baseURL, prof.Auth())

	if asJSON {
		payload := setupPayload(profileName, displayName, baseURL, projectRoot, prof.RaptorProfile, local, state)
		if raptorW != nil {
			payload["raptor_credentials"] = map[string]any{
				"path": raptorW.Path, "section": raptorW.Section, "changed": raptorW.Changed,
			}
		}
		if raptorWarn != "" {
			payload["raptor_credentials_warning"] = raptorWarn
		}
		return render.JSON(out, payload)
	}
	if local {
		fmt.Fprintf(out, "\n✓ Logged in as %s and pinned profile %q to %s\n", displayName, profileName, projectRoot)
	} else {
		fmt.Fprintf(out, "\n✓ Logged in as %s (profile: %s, url: %s)\n", displayName, profileName, baseURL)
	}
	switch {
	case raptorWarn != "":
		fmt.Fprintf(out, "Warning: %s\n", raptorWarn)
	case raptorW != nil && raptorW.Changed:
		fmt.Fprintf(out, "✓ raptor profile %q saved to %s\n", raptorW.Section, raptorW.Path)
	}
	return nil
}

// raptorSection names the ~/.facets/credentials section a PAT login writes:
// the pairing when one exists, else the praxis profile's own name.
func raptorSection(profileName, pin string) string {
	if pin != "" {
		return pin
	}
	return profileName
}

// writeRaptorCredentials mirrors a control-plane PAT into raptor's store, next
// to the project pointer for a --local login. Only PAT (facets) profiles have a
// credential raptor can use; API-key profiles write nothing. A failed write is
// a warning, never a failed login — praxis itself is authenticated. Nil result
// with an empty warning means nothing applied.
func writeRaptorCredentials(profileName string, prof credentials.Profile, projectRoot string) (*raptorstate.Written, string) {
	if prof.AuthMode != credentials.AuthModeBasic {
		return nil, ""
	}
	dir := ""
	if projectRoot != "" {
		dir = filepath.Dir(projectRoot)
	}
	w, err := raptorstate.Write(raptorSection(profileName, prof.RaptorProfile), prof.URL, prof.Username, prof.Token, dir)
	if err != nil {
		return nil, fmt.Sprintf("could not save the raptor profile (%v); run `raptor login` to set it up", err)
	}
	if w.Replaced != "" {
		fmt.Fprintf(os.Stderr, "raptor profile %q now points at %s (was %s).\n", w.Section, prof.URL, w.Replaced)
	}
	return &w, ""
}

// resolveRaptorPairing decides the raptor_profile value to persist:
// the --raptor-profile flag when given (validated best-effort — warnings,
// never a failed login), else whatever the profile already stored, so a
// plain re-login never drops an existing pairing. Warnings go to stderr in
// both output modes so they never corrupt --json output.
func resolveRaptorPairing(profileName, baseURL string) string {
	if loginRaptorProfile == "" {
		return raptorPin(profileName)
	}
	// Validation is advisory: raptor's credentials are the user's to manage,
	// and they may run `raptor login` after this.
	ok, cpURL := raptorstate.HasProfile(loginRaptorProfile)
	switch {
	case !ok:
		fmt.Fprintf(os.Stderr,
			"Warning: raptor profile %q not found in ~/.facets/credentials — pairing saved anyway; run `raptor login` and create it.\n",
			loginRaptorProfile)
	case !raptorstate.MatchesHost(baseURL, cpURL):
		fmt.Fprintf(os.Stderr,
			"Warning: raptor profile %q points at %s, which is a different host than this praxis profile (%s). Pairing saved anyway.\n",
			loginRaptorProfile, cpURL, baseURL)
	}
	return loginRaptorProfile
}

// raptorPin returns the raptor profile this login is paired with: the
// --raptor-profile flag when given, else whatever the profile already stored.
// The PAT lookup and the persisted pairing share it, so `--raptor-profile X` on
// a first login can't authenticate as one profile and save a pairing to another.
func raptorPin(profileName string) string {
	if loginRaptorProfile != "" {
		return loginRaptorProfile
	}
	store, err := credentials.Load()
	if err != nil {
		return ""
	}
	return store[profileName].RaptorProfile
}

// activateProfile makes profileName the active profile and pins
// paths.ActiveRoot to the matching scope, returning the project root (empty
// when global) and a restore func the caller MUST defer.
//
// The two halves are inseparable, which is why they live in one helper: the
// pointer decides which profile is active, and ActiveRoot decides where that
// profile's skills, receipt, and MCP snapshot land. Flipping one without the
// other is how you end up with profile A active and profile B's org skills on
// disk — the exact invariant runPostAuthSetup exists to keep.
//
//   - local: write the project pointer (<cwd>/.praxis, created if needed and
//     required to be under home) and pin the root there, so the install is
//     project-scoped. The global pointer is left alone.
//   - global: flip ~/.praxis/config.json and pin the root to home, so the
//     install stays user-level even when run from inside a project tree.
//
// Shared by `praxis login` and `praxis profiles use`.
func activateProfile(profileName string, local bool) (string, func(), error) {
	noop := func() {}
	if local {
		root, err := credentials.SetActiveLocal(profileName)
		if err != nil {
			return "", noop, fmt.Errorf("pin profile to this directory: %w", err)
		}
		return root, paths.OverrideActiveRoot(root), nil
	}
	if err := credentials.SetActive(profileName); err != nil {
		return "", noop, fmt.Errorf("set active profile: %w", err)
	}
	home, err := paths.Dir()
	if err != nil {
		// Home is unresolvable, so there's nothing to pin to — ActiveRoot
		// will fail the same way downstream and postAuthSetup reports it.
		return "", noop, nil
	}
	return "", paths.OverrideActiveRoot(home), nil
}

// setupPayload builds the JSON envelope for commands that make a profile
// active and re-run postAuthSetup. `praxis login` and `praxis profiles use`
// share it verbatim so an AI host can parse either one with the same reader —
// including raptor_profile, so a switch surfaces the pairing login reported.
func setupPayload(profileName, username, baseURL, projectRoot, raptorProfile string, local bool, state postAuthState) map[string]any {
	payload := map[string]any{
		"ok":               true,
		"profile":          profileName,
		"username":         username,
		"url":              baseURL,
		"scope":            scopeLabel(local),
		"meta_skill":       state.metaSkill,
		"catalog_skills":   state.catalogSkills,
		"removed_skills":   state.removedSkills,
		"agents":           state.agents,
		"removed_agents":   state.removedAgents,
		"snapshot_path":    state.snapshotPath,
		"snapshot_warning": state.snapshotWarning,
	}
	if projectRoot != "" {
		payload["project_root"] = projectRoot
	}
	if raptorProfile != "" {
		payload["raptor_profile"] = raptorProfile
	}
	return payload
}

type authMeResponse struct {
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	Username string `json:"username"`

	// canonicalBaseURL is the deployment base URL the /auth/me call
	// actually landed on after following redirects. Login persists this
	// instead of the URL the user typed, so later MCP invokes never pay
	// that redirect.
	// Empty when a test stub doesn't set it — callers fall back to the
	// URL they already have.
	canonicalBaseURL string
}

// applyCanonical persists the deployment base /auth/me actually landed on after
// redirects, so a stale stored URL self-heals on the next login (issue #19-A)
// instead of making every later MCP invoke pay the hop. Every verify path calls
// it; a test stub that leaves canonicalBaseURL empty keeps the URL it had.
func applyCanonical(prof credentials.Profile, user *authMeResponse) credentials.Profile {
	if user.canonicalBaseURL != "" {
		prof.URL = user.canonicalBaseURL
	}
	return prof
}

// errTokenRejected signals that the server actively rejected the stored
// token — HTTP 401/403, i.e. the token is genuinely expired or revoked.
// It is deliberately distinct from a transient failure (timeout,
// connection refused, 5xx), where the token's validity is simply unknown
// because the server never got a chance to vouch for it. The login reuse
// path treats the two very differently: a rejected token falls back to the
// browser, an unreachable server aborts and leaves the token intact.
var errTokenRejected = errors.New("token rejected by server")

// fetchAuthMe is the seam: tests swap it to avoid hitting a real server.
var fetchAuthMe = func(baseURL string, auth map[string]string) (*authMeResponse, error) {
	client := httpclient.New(10 * time.Second)
	req, err := http.NewRequest("GET", baseURL+"/ai-api/auth/me", nil)
	if err != nil {
		return nil, err
	}
	for k, v := range auth {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// The server reached a verdict: this token is no good. Wrap the
		// sentinel so callers can errors.Is it apart from transient noise.
		return nil, fmt.Errorf("%w (HTTP %d from %s/ai-api/auth/me)", errTokenRejected, resp.StatusCode, baseURL)
	}
	if resp.StatusCode != http.StatusOK {
		// 5xx and other unexpected statuses are server-side trouble, not a
		// verdict on the token — left unwrapped so they read as transient.
		return nil, fmt.Errorf("HTTP %d from %s/ai-api/auth/me", resp.StatusCode, baseURL)
	}
	var me authMeResponse
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		return nil, err
	}
	// resp.Request is the FINAL request after the client followed any
	// redirects; strip the known endpoint path to recover the canonical
	// deployment base (preserving any path prefix the deployment lives
	// under). Fall back to what the caller passed if the shape is ever
	// unexpected.
	me.canonicalBaseURL = baseURL
	if final := resp.Request.URL.String(); strings.HasSuffix(final, "/ai-api/auth/me") {
		if base := strings.TrimRight(strings.TrimSuffix(final, "/ai-api/auth/me"), "/"); base != "" {
			me.canonicalBaseURL = base
		}
	}
	return &me, nil
}

// scopeLabel renders the install scope for JSON output.
func scopeLabel(local bool) string {
	if local {
		return "local"
	}
	return "global"
}

func randomNonce() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var openBrowser = func(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "linux":
		cmd = exec.Command("xdg-open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	return cmd.Start()
}
