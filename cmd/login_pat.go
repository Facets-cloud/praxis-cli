package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/httpclient"
)

// facetsAuthMode is the server's auth_mode when it validates control-plane PATs.
const facetsAuthMode = "facets"

// authModeTimeout bounds the auth-mode probe; an unanswered probe just means
// "keep the API-key flow", so there is nothing to wait for.
const authModeTimeout = 3 * time.Second

// patPageURL is the page that completes a control-plane sign-in for the CLI.
// It posts the nonce with the browser's own session cookie; the server resolves
// the user's PAT from that. Nothing is ever displayed for the user to copy.
func patPageURL(baseURL, nonce string) string {
	return fmt.Sprintf("%s/ui/ai/cli-login?cli_session=%s", normalizeBaseURL(baseURL), nonce)
}

// authStatus is the public /ai-api/auth/status answer the login chain branches
// on. PATHandshake false (including "field absent") keeps the API-key flow.
type authStatus struct {
	Mode         string
	PATHandshake bool
}

// fetchAuthStatus reads the deployment's auth mode and CLI capabilities from
// GET /ai-api/auth/status, which is public and never 401s by contract. Returns
// a zero value for any unusable answer — unreachable, non-200, unparseable —
// which callers read as "not facets mode".
var fetchAuthStatus = func(baseURL string) authStatus {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/ai-api/auth/status", nil)
	if err != nil {
		return authStatus{}
	}
	resp, err := httpclient.New(authModeTimeout).Do(req)
	if err != nil {
		return authStatus{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return authStatus{}
	}
	var body struct {
		AuthMode        string `json:"auth_mode"`
		CLIPATHandshake bool   `json:"cli_pat_handshake"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return authStatus{}
	}
	return authStatus{
		Mode:         strings.ToLower(strings.TrimSpace(body.AuthMode)),
		PATHandshake: body.CLIPATHandshake,
	}
}

// Prompt seams: tests drive the interactive flow without a terminal.
var (
	stdinIsTTY = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	// readLine reads one line unbuffered.
	readLine = func() (string, error) {
		var line []byte
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if buf[0] == '\n' {
					return string(line), nil
				}
				line = append(line, buf[0])
			}
			if err != nil {
				if len(line) == 0 {
					return "", err
				}
				return string(line), nil
			}
		}
	}
)

// interactivePATEligible reports whether login can obtain a control-plane PAT
// through the browser handshake. Shared with --dry-run so the report cannot
// disagree with the chain, and ordered cheapest-first.
func interactivePATEligible(baseURL string) bool {
	if !patTransportOK(baseURL) {
		return false
	}
	st := fetchAuthStatus(baseURL)
	return st.Mode == facetsAuthMode && st.PATHandshake
}

// tryInteractivePAT signs the user in through the control plane and takes the
// PAT the server resolves for them — nothing is shown, so nothing is pasted.
// It sits between raptor's stored PAT and minting a Praxis API key.
//
// handled=false (never an error) sends the caller on to the API-key flow: the
// deployment does not serve the handshake, the browser was never completed, or
// the PAT the server returned was not accepted.
func tryInteractivePAT(out io.Writer, asJSON bool, profileName, baseURL string, timeout time.Duration, local bool) (bool, error) {
	if !interactivePATEligible(baseURL) {
		return false, nil
	}

	nonce := randomNonce()
	page := patPageURL(baseURL, nonce)
	fmt.Fprintln(os.Stderr, "Opening the control plane to sign in…")
	fmt.Fprintf(os.Stderr, "  %s\n", page)
	if err := openBrowser(page); err != nil {
		fmt.Fprintf(os.Stderr, "\nCouldn't auto-open browser (%v). Open the URL above manually.\n", err)
	}
	fmt.Fprintf(os.Stderr, "Waiting for sign-in (timeout %s)…\n", timeout)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cred, err := pollSessionFn(ctx, baseURL, nonce, pollInterval)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Control-plane sign-in did not complete (%v); opening browser to create a Praxis API key…\n", err)
		return false, nil
	}
	if cred.Username == "" {
		// A PAT with no identity header could never authenticate.
		fmt.Fprintln(os.Stderr,
			"Control plane returned no username for the token; opening browser to create a Praxis API key…")
		return false, nil
	}

	prof := credentials.FacetsProfile(baseURL, cred.Username, cred.Token)
	user, err := fetchAuthMe(baseURL, prof.Auth())
	if err != nil {
		verdict := "could not be verified"
		if errors.Is(err, errTokenRejected) {
			verdict = "was not accepted"
		}
		fmt.Fprintf(os.Stderr,
			"The control-plane token for %s %s at %s (%v); opening browser to create a Praxis API key…\n",
			cred.Username, verdict, baseURL, err)
		return false, nil
	}
	return true, persistVerified(out, asJSON, profileName, prof, user, cred.Username, local)
}

// promptLoginURL asks for the control-plane URL when no flag, saved profile or
// raptor profile supplied one — the first thing `raptor login` asks. Returns ""
// (caller keeps its usage error) on --json, without a TTY, or on an empty
// answer. A bare host gets https://.
func promptLoginURL(asJSON bool) string {
	if asJSON || !stdinIsTTY() {
		return ""
	}
	u := prompt("Control plane URL (e.g. https://<account-id>.console.facets.cloud): ", readLine)
	if u == "" {
		return ""
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "https://" + u
	}
	return normalizeBaseURL(u)
}

// prompt writes the label to stderr — never stdout, which a caller may be
// parsing — and returns the trimmed answer. A read error reads as an empty
// answer, which is the caller's "skip this tier" signal.
func prompt(label string, read func() (string, error)) string {
	fmt.Fprint(os.Stderr, label)
	s, err := read()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}
