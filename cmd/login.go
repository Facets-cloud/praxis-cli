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

var (
	loginProfile string
	loginURL     string
	loginToken   string
	loginJSON    bool
	loginTimeout time.Duration
)

func init() {
	loginCmd.Flags().StringVar(&loginProfile, "profile", "", "save under this profile name (default: \"default\")")
	loginCmd.Flags().StringVar(&loginURL, "url", "", "Praxis deployment URL (default: existing profile URL or "+credentials.DefaultURL+")")
	loginCmd.Flags().StringVar(&loginToken, "token", "", "skip browser flow; save and verify the given API key directly")
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
  2. Open a browser to create a Praxis API key (or use --token to skip).
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
		baseURL := resolveLoginURL(profileName, loginURL)

		if loginToken != "" {
			return saveAndVerifyToken(out, asJSON, profileName, baseURL, loginToken)
		}
		return browserSessionPollLogin(out, asJSON, profileName, baseURL, loginTimeout)
	},
}

// resolveLoginURL resolves the URL for a NEW or EXISTING profile during
// login: explicit --url > existing profile's saved URL > built-in default.
func resolveLoginURL(profileName, flagURL string) string {
	if flagURL != "" {
		return flagURL
	}
	store, _ := credentials.Load()
	if p, ok := store[profileName]; ok && p.URL != "" {
		return p.URL
	}
	return credentials.DefaultURL
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
	client := &http.Client{Timeout: 5 * time.Second}

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
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", pollPending, err
		}
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

func saveAndVerifyToken(out io.Writer, asJSON bool, profileName, baseURL, token string) error {
	user, err := fetchAuthMe(baseURL, token)
	if err != nil {
		render.PrintError(out, asJSON,
			fmt.Sprintf("token validation failed: %v", err),
			"the API key may be invalid, revoked, or the URL is wrong",
			exitcode.Auth)
		os.Exit(exitcode.Auth)
	}

	prof := credentials.Profile{
		URL:      baseURL,
		Username: user.Email,
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
	postAuthState := runPostAuthSetup(out, asJSON, baseURL, token)

	if asJSON {
		return render.JSON(out, map[string]any{
			"ok":               true,
			"profile":          profileName,
			"username":         user.Email,
			"url":              baseURL,
			"meta_skill":       postAuthState.metaSkill,
			"catalog_skills":   postAuthState.catalogSkills,
			"removed_skills":   postAuthState.removedSkills,
			"snapshot_path":    postAuthState.snapshotPath,
			"snapshot_warning": postAuthState.snapshotWarning,
		})
	}
	fmt.Fprintf(out, "\n✓ Logged in as %s (profile: %s, url: %s)\n", user.Email, profileName, baseURL)
	return nil
}

type authMeResponse struct {
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	Username string `json:"username"`
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
