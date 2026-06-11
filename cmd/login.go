package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
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
	loginProfile string
	loginURL     string
	loginToken   string
	loginForce   bool
	loginJSON    bool
	loginTimeout time.Duration
)

// browserLoginFn and postAuthSetup are package-level seams so tests can
// exercise login's path selection (reuse vs. browser) and persistence
// without opening a browser, hitting the network, or installing skills.
var (
	browserLoginFn = browserSessionPollLogin
	postAuthSetup  = runPostAuthSetup
)

func init() {
	loginCmd.Flags().StringVar(&loginProfile, "profile", "", "save under this profile name (default: \"default\")")
	loginCmd.Flags().StringVar(&loginURL, "url", "", "Praxis deployment URL (default: existing profile URL or "+credentials.DefaultURL+")")
	loginCmd.Flags().StringVar(&loginToken, "token", "", "skip browser flow; save and verify the given API key directly")
	loginCmd.Flags().BoolVar(&loginForce, "force", false, "skip reusing a stored token; always open the browser")
	loginCmd.Flags().BoolVar(&loginJSON, "json", false, "JSON output")
	loginCmd.Flags().DurationVar(&loginTimeout, "timeout", 90*time.Second, "max time to wait for browser callback")
	rootCmd.AddCommand(loginCmd)
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate, install meta-skill, and sync this profile's org catalog",
	Long: `Single entry point for setup. Login does, in order:

  1. Install the praxis meta-skill into every detected AI host
     (~/.claude/skills/praxis, ~/.agents/skills/praxis,
      ~/.gemini/skills/praxis) — idempotent.
  2. Reuse the active profile's stored token when it's still valid for
     this URL (no browser); otherwise open a browser to create a Praxis
     API key. Use --token to supply a key directly, or --force to always
     re-authenticate via the browser.
  3. Save credentials and flip the active profile pointer.
  4. Wipe any praxis-* org skills from the previous profile.
  5. Fetch this profile's skill catalog from the server and install
     each entry as praxis-<name> across all detected AI hosts.
  6. Refresh ~/.praxis/mcp-tools.json from the server's MCP manifest.

Multiple deployments? Use --profile to keep them separate:

  praxis login                                    → "default"
  praxis login --profile acme --url https://...   → "acme"
  praxis login --profile bigcorp --url https://.. → "bigcorp"

Re-running login (with the same profile or a different one) is the
canonical way to refresh skills + manifest snapshot. There is no
separate refresh command in v0.7.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(loginJSON, false, out)

		profileName := loginProfile
		if profileName == "" {
			profileName = credentials.DefaultProfileName
		}
		baseURL, err := resolveLoginURL(profileName, loginURL)
		if err != nil {
			render.PrintError(out, asJSON, err.Error(),
				"pass --url <https://your-praxis-deployment> to create this profile",
				exitcode.Usage)
			return err
		}

		// --token is the explicit non-browser path: verify the supplied
		// key and persist it, unchanged by the reuse logic.
		if loginToken != "" {
			return saveAndVerifyToken(out, asJSON, profileName, baseURL, loginToken)
		}

		// Smart default: if the active profile already has a token valid
		// for this URL, refresh skills + manifest without a browser hop.
		// --force opts out and always re-authenticates via the browser.
		if !loginForce {
			reused, rerr := tryReuseStoredToken(out, asJSON, profileName, baseURL)
			if reused {
				return rerr
			}
		}
		return browserLoginFn(out, asJSON, profileName, baseURL, loginTimeout)
	},
}

// resolveLoginURL resolves the URL for a NEW or EXISTING profile during
// login: explicit --url > existing profile's saved URL > built-in default.
//
// A NEW *named* profile (one not present in the store) without --url is
// an error — there's no URL to reuse and guessing askpraxis.ai for a
// named deployment would be wrong. The "default" profile keeps the
// built-in fallback so a zero-config first run still works.
func resolveLoginURL(profileName, flagURL string) (string, error) {
	if flagURL != "" {
		return flagURL, nil
	}
	store, _ := credentials.Load()
	if p, ok := store[profileName]; ok && p.URL != "" {
		return p.URL, nil
	}
	if profileName == credentials.DefaultProfileName {
		return credentials.DefaultURL, nil
	}
	return "", fmt.Errorf("profile %q does not exist yet; pass --url to create it", profileName)
}

// tryReuseStoredToken attempts a no-browser login using the token already
// stored for profileName.
//
// It returns reused=true only when it has TAKEN OWNERSHIP of the login —
// the stored token verified and the persist+setup tail ran (the returned
// error is that tail's result, nil on success). The caller must NOT fall
// back to the browser in that case.
//
// reused=false means no reuse was possible — no stored token, the profile
// is being re-targeted at a different URL, or the stored token failed
// verification (expired/revoked). The error is always nil here; the caller
// should proceed to the browser flow. A verification failure is reported
// as a one-line notice on stderr, not an error, so the fallback is smooth.
func tryReuseStoredToken(out io.Writer, asJSON bool, profileName, baseURL string) (bool, error) {
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

	user, err := fetchAuthMe(baseURL, prof.Token)
	if err != nil {
		if !asJSON {
			fmt.Fprintf(os.Stderr,
				"Stored token for profile %q is no longer valid (%v); opening browser…\n",
				profileName, err)
		}
		return false, nil // graceful fallback to the browser
	}
	// Persist the canonical (post-redirect) host so a stale stored URL
	// self-heals on the next login (issue #19-A).
	if user.canonicalBaseURL != "" {
		baseURL = user.canonicalBaseURL
	}
	return true, persistAndSetup(out, asJSON, profileName, baseURL, prof.Token, user.Email)
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
func browserSessionPollLogin(out io.Writer, asJSON bool, profileName, baseURL string, timeout time.Duration) error {
	sessionNonce := randomNonce()

	openURL, err := buildLoginURL(baseURL, sessionNonce, suggestedKeyName())
	if err != nil {
		render.PrintError(out, asJSON, err.Error(),
			"check the --url value (or PRAXIS_URL) — it must be a valid URL",
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
	return saveAndVerifyToken(out, asJSON, profileName, baseURL, key)
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
	client := &http.Client{Timeout: pollRequestTimeout}

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

// saveAndVerifyToken verifies a freshly-obtained token (from --token or
// the browser flow) and persists it. A verification failure here is fatal
// — the user explicitly supplied this key, so there's no graceful
// fallback to attempt.
func saveAndVerifyToken(out io.Writer, asJSON bool, profileName, baseURL, token string) error {
	user, err := fetchAuthMe(baseURL, token)
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
	return persistAndSetup(out, asJSON, profileName, baseURL, token, user.Email)
}

// persistAndSetup saves the verified token under profileName, flips the
// active-profile pointer, runs post-auth setup (meta-skill + catalog +
// MCP manifest), and renders the result. It is the shared tail of both
// the verify-then-save path (saveAndVerifyToken) and the reuse path
// (tryReuseStoredToken). Returning an error rather than os.Exit lets the
// reuse path stay non-fatal up to this point.
func persistAndSetup(out io.Writer, asJSON bool, profileName, baseURL, token, email string) error {
	prof := credentials.Profile{
		URL:      baseURL,
		Username: email,
		Token:    token,
	}
	if err := credentials.Put(profileName, prof); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}
	if err := credentials.SetActive(profileName); err != nil {
		return fmt.Errorf("set active profile: %w", err)
	}

	// Post-auth: install meta-skill, wipe previous org skills, install
	// this profile's catalog, refresh the MCP tools snapshot.
	state := postAuthSetup(out, asJSON, baseURL, token, false)

	if asJSON {
		return render.JSON(out, map[string]any{
			"ok":               true,
			"profile":          profileName,
			"username":         email,
			"url":              baseURL,
			"meta_skill":       state.metaSkill,
			"catalog_skills":   state.catalogSkills,
			"removed_skills":   state.removedSkills,
			"agents":           state.agents,
			"removed_agents":   state.removedAgents,
			"snapshot_path":    state.snapshotPath,
			"snapshot_warning": state.snapshotWarning,
		})
	}
	fmt.Fprintf(out, "\n✓ Logged in as %s (profile: %s, url: %s)\n", email, profileName, baseURL)
	return nil
}

type authMeResponse struct {
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	Username string `json:"username"`

	// canonicalBaseURL is the deployment base URL the /auth/me call
	// actually landed on after following redirects (e.g. the apex
	// askpraxis.ai 301s to www). Login persists this instead of the URL
	// the user typed, so later MCP invokes never pay that redirect.
	// Empty when a test stub doesn't set it — callers fall back to the
	// URL they already have.
	canonicalBaseURL string
}

// fetchAuthMe is the seam: tests swap it to avoid hitting a real server.
var fetchAuthMe = func(baseURL, token string) (*authMeResponse, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", baseURL+"/ai-api/auth/me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
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
