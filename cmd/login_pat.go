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
// on. PATHandshake reports whether the deployment serves the zero-copy sign-in;
// it defaults false, so a deployment too old to send the field keeps the
// API-key flow it had before.
type authStatus struct {
	Mode         string
	PATHandshake bool
}

// fetchAuthStatus reads the deployment's auth mode and CLI capabilities from
// GET /ai-api/auth/status, which is public and never 401s by contract. Returns
// a zero value for any unusable answer — unreachable, non-200, unparseable —
// which callers read as "not facets mode".
//
// The capability has to come from here rather than probing the route itself:
// CSRF middleware runs before routing, so a deployment without the route and
// one with it both answer 403 to an unauthenticated POST.
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
	readSecret = func() (string, error) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		return string(b), err
	}
	// readLine reads one line unbuffered. A bufio.Reader would consume past the
	// newline into its own buffer, and readSecret reads the raw fd — so a
	// username and token pasted together would lose the token.
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
// disagree with the chain. Ordered cheapest-first: the network probes run only
// once the local gate passes.
//
// No TTY requirement: the user types nothing, so an AI host spawning praxis
// gets the same flow — and with it the raptor credentials a Praxis API key
// cannot provide.
func interactivePATEligible(baseURL string, asJSON bool) bool {
	if !patTransportOK(baseURL) {
		return false
	}
	st := fetchAuthStatus(baseURL)
	return st.Mode == facetsAuthMode && st.PATHandshake
}

// tryInteractivePAT signs the user in through the control plane and takes the
// control-plane PAT the server resolves for them — no token is ever shown, so
// nothing is pasted back. It is the tier between raptor's stored PAT and
// minting a Praxis API key, so a control-plane PAT is what praxis
// authenticates with whether or not raptor ever ran here. That also keeps the
// credential a PAT, which is what lets login configure raptor too.
//
// handled=false (never an error) sends the caller on to the Praxis API-key
// flow: the deployment does not serve the handshake, the browser was never
// completed, or the PAT the server returned was not accepted.
func tryInteractivePAT(out io.Writer, asJSON bool, profileName, baseURL string, local bool) (bool, error) {
	if !interactivePATEligible(baseURL, asJSON) {
		return false, nil
	}

	nonce := randomNonce()
	page := patPageURL(baseURL, nonce)
	fmt.Fprintln(os.Stderr, "Opening the control plane to sign in…")
	fmt.Fprintf(os.Stderr, "  %s\n", page)
	if err := openBrowser(page); err != nil {
		fmt.Fprintf(os.Stderr, "\nCouldn't auto-open browser (%v). Open the URL above manually.\n", err)
	}
	fmt.Fprintf(os.Stderr, "Waiting for sign-in (timeout %s)…\n", loginTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	defer cancel()

	cred, err := pollSessionFn(ctx, baseURL, nonce, pollInterval)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Control-plane sign-in did not complete (%v); opening browser to create a Praxis API key…\n", err)
		return false, nil
	}
	if cred.Username == "" {
		// An API-key deposit landed on this nonce. Only our own page posts to
		// it, so this means the deployment answered the probe but served the
		// older handshake — fall through rather than build a PAT profile with
		// no identity header, which could never authenticate.
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
