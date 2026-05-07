package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

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
	Short: "Authenticate with Praxis (browser-callback flow)",
	Long: `Open a browser to create a Praxis API key, then capture the new
key via a one-shot localhost listener. The user clicks "Create Key" once;
this command handles the rest.

For non-interactive use (e.g. CI, or AI hosts that already have a token),
pass --token sk_live_…

Multiple deployments? Use --profile to keep them separate:

  praxis login                                    → saves to "default"
  praxis login --profile acme --url https://...   → saves to "acme"
  praxis login --profile vymo --url https://...   → saves to "vymo"

Then switch contexts with:

  praxis use acme                  (sets active profile)
  PRAXIS_PROFILE=acme praxis ...   (one-shell override)
  praxis ... --profile acme        (one-command override)`,
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
		return browserCallbackLogin(out, asJSON, profileName, baseURL, loginTimeout)
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

// browserCallbackLogin opens the browser, waits up to timeout for the
// browser to POST the new key to the localhost listener, then saves +
// verifies the captured token under profileName.
func browserCallbackLogin(out io.Writer, asJSON bool, profileName, baseURL string, timeout time.Duration) error {
	sessionNonce := randomNonce()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start localhost listener: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	type captured struct {
		key string
		err error
	}
	resultCh := make(chan captured, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/key", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", baseURL)
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Session      string `json:"session"`
			PlaintextKey string `json:"plaintext_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			resultCh <- captured{err: fmt.Errorf("bad callback body: %w", err)}
			return
		}
		if body.Session != sessionNonce {
			http.Error(w, "session mismatch", http.StatusForbidden)
			resultCh <- captured{err: fmt.Errorf("session nonce mismatch")}
			return
		}
		if body.PlaintextKey == "" {
			http.Error(w, "missing key", http.StatusBadRequest)
			resultCh <- captured{err: fmt.Errorf("missing plaintext_key")}
			return
		}
		w.WriteHeader(http.StatusNoContent)
		resultCh <- captured{key: body.PlaintextKey}
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	openURL, err := buildLoginURL(baseURL, port, sessionNonce)
	if err != nil {
		render.PrintError(out, asJSON, err.Error(),
			"check the --url value (or PRAXIS_URL) — it must be a valid URL",
			exitcode.Usage)
		os.Exit(exitcode.Usage)
	}
	fmt.Fprintln(os.Stderr, "Opening browser to create a Praxis API key…")
	fmt.Fprintln(os.Stderr, "  ", openURL)
	fmt.Fprintf(os.Stderr, "Waiting for callback (timeout %s)…\n", timeout)
	if err := openBrowser(openURL); err != nil {
		fmt.Fprintf(os.Stderr, "\nCouldn't auto-open browser (%v). Open the URL above manually.\n", err)
	}

	select {
	case res := <-resultCh:
		if res.err != nil {
			render.PrintError(out, asJSON, res.err.Error(),
				"the browser callback failed", exitcode.Auth)
			os.Exit(exitcode.Auth)
		}
		return saveAndVerifyToken(out, asJSON, profileName, baseURL, res.key)
	case <-time.After(timeout):
		render.PrintError(out, asJSON, "login timed out",
			"finish the API key creation in the browser within the timeout", exitcode.Auth)
		os.Exit(exitcode.Auth)
	}
	return nil
}

func buildLoginURL(baseURL string, callbackPort int, sessionNonce string) (string, error) {
	u, err := url.Parse(baseURL + "/ui/ai/settings/api-keys")
	if err != nil {
		return "", fmt.Errorf("invalid login URL %q: %w", baseURL, err)
	}
	q := u.Query()
	q.Set("cli_callback", fmt.Sprintf("http://127.0.0.1:%d/key", callbackPort))
	q.Set("cli_session", sessionNonce)
	q.Set("suggested_name", "praxis-cli")
	u.RawQuery = q.Encode()
	return u.String(), nil
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

	if asJSON {
		return render.JSON(out, map[string]any{
			"ok":       true,
			"profile":  profileName,
			"username": user.Email,
			"url":      baseURL,
		})
	}
	fmt.Fprintf(out, "✓ Logged in as %s (profile: %s, url: %s)\n", user.Email, profileName, baseURL)
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
