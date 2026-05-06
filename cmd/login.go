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
	"path/filepath"
	"runtime"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/config"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

var (
	loginURL     string
	loginToken   string
	loginJSON    bool
	loginTimeout time.Duration
)

func init() {
	loginCmd.Flags().StringVar(&loginURL, "url", "", "Praxis deployment URL (overrides PRAXIS_URL/config)")
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
pass --token sk_live_…`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(loginJSON, false, out)

		resolved, err := config.ResolveURL(loginURL)
		if err != nil {
			return err
		}
		baseURL := resolved.URL

		if loginToken != "" {
			return saveAndVerifyToken(out, asJSON, baseURL, loginToken)
		}

		return browserCallbackLogin(out, asJSON, baseURL, loginTimeout)
	},
}

// browserCallbackLogin opens the browser, waits up to timeout for the
// browser to POST the new key to the localhost listener, then saves +
// verifies the captured token.
func browserCallbackLogin(out io.Writer, asJSON bool, baseURL string, timeout time.Duration) error {
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
		// CORS for the cross-origin POST from baseURL.
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

	openURL := buildLoginURL(baseURL, port, sessionNonce)
	// Print progress to stderr so even --json callers (and tests piping
	// stdout) can see what URL was opened. Stdout stays reserved for the
	// final structured result.
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
				"the browser callback failed; check the page console", exitcode.Auth)
			os.Exit(exitcode.Auth)
		}
		return saveAndVerifyToken(out, asJSON, baseURL, res.key)
	case <-time.After(timeout):
		render.PrintError(out, asJSON, "login timed out",
			"finish the API key creation in the browser within the timeout", exitcode.Auth)
		os.Exit(exitcode.Auth)
	}
	return nil
}

// buildLoginURL composes the api-keys page URL with the cli-callback
// query params the agent-factory ApiKeyCreateModal reads.
func buildLoginURL(baseURL string, callbackPort int, sessionNonce string) string {
	u, _ := url.Parse(baseURL + "/ui/ai/settings/api-keys")
	q := u.Query()
	q.Set("cli_callback", fmt.Sprintf("http://127.0.0.1:%d/key", callbackPort))
	q.Set("cli_session", sessionNonce)
	q.Set("suggested_name", "praxis-cli")
	u.RawQuery = q.Encode()
	return u.String()
}

// Credentials is the on-disk schema at ~/.praxis/credentials.
type Credentials struct {
	URL         string `json:"url"`
	AccessToken string `json:"access_token"`
	UserID      string `json:"user_id,omitempty"`
	UserEmail   string `json:"user_email,omitempty"`
}

func saveAndVerifyToken(out io.Writer, asJSON bool, baseURL, token string) error {
	user, err := fetchAuthMe(baseURL, token)
	if err != nil {
		render.PrintError(out, asJSON,
			fmt.Sprintf("token validation failed: %v", err),
			"the API key may be invalid, revoked, or the URL is wrong",
			exitcode.Auth)
		os.Exit(exitcode.Auth)
	}

	credPath, err := paths.Credentials()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(credPath), 0700); err != nil {
		return err
	}
	creds := Credentials{
		URL:         baseURL,
		AccessToken: token,
		UserID:      user.UserID,
		UserEmail:   user.Email,
	}
	data, _ := json.MarshalIndent(creds, "", "  ")
	if err := os.WriteFile(credPath, data, 0600); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}

	if asJSON {
		return render.JSON(out, map[string]any{
			"ok":         true,
			"user_email": user.Email,
			"user_id":    user.UserID,
			"url":        baseURL,
		})
	}
	fmt.Fprintf(out, "✓ Logged in as %s (%s)\n", user.Email, baseURL)
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

// openBrowser launches the user's default browser. Caller doesn't wait
// for it to exit — browser stays open as a child of init.
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
