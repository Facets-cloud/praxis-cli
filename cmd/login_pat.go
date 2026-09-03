package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/httpclient"
	"github.com/Facets-cloud/praxis-cli/internal/raptorstate"
	"github.com/Facets-cloud/praxis-cli/internal/render"
)

// facetsAuthMode is the server's auth_mode when it validates control-plane PATs.
const facetsAuthMode = "facets"

// authModeTimeout bounds the auth-mode probe; an unanswered probe just means
// "keep the API-key flow", so there is nothing to wait for.
const authModeTimeout = 3 * time.Second

// patPageURL is the control-plane page that mints a personal access token — the
// same page `raptor login` opens, so one token serves both CLIs.
func patPageURL(baseURL string) string {
	return normalizeBaseURL(baseURL) + "/v2/home#personal-access-tokens"
}

// fetchAuthMode reports the deployment's auth mode from GET
// /ai-api/auth/status, which is public and never 401s by contract. Returns ""
// for any unusable answer — unreachable, non-200, unparseable, or a deployment
// old enough not to serve it — which callers read as "not facets mode", so an
// older deployment keeps the behavior it had before this probe existed.
var fetchAuthMode = func(baseURL string) string {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/ai-api/auth/status", nil)
	if err != nil {
		return ""
	}
	resp, err := httpclient.New(authModeTimeout).Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var body struct {
		AuthMode string `json:"auth_mode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(body.AuthMode))
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

// interactivePATEligible reports whether login can ask for a control-plane PAT.
// Shared with --dry-run so the report cannot disagree with the chain. Ordered
// cheapest-first: the network probe runs only once the local gates pass.
func interactivePATEligible(baseURL string, asJSON bool) bool {
	if asJSON || !stdinIsTTY() || !patTransportOK(baseURL) {
		return false
	}
	return fetchAuthMode(baseURL) == facetsAuthMode
}

// tryInteractivePAT opens the control plane's personal-access-token page and
// takes the token the user creates there, the way `raptor login` does. It is
// the tier between raptor's stored PAT and minting a Praxis API key, so a
// control-plane PAT is what praxis authenticates with whether or not raptor
// ever ran here.
//
// handled=false (never an error) sends the caller on to the Praxis API-key
// flow: not eligible, an empty answer at either prompt, or a PAT the server
// would not take. That last case matches tryFacetsPAT rather than failing the
// login — the API key is the final fallback, so the chain keeps walking.
func tryInteractivePAT(out io.Writer, asJSON bool, profileName, baseURL string, local bool) (bool, error) {
	if !interactivePATEligible(baseURL, asJSON) {
		return false, nil
	}

	page := patPageURL(baseURL)
	fmt.Fprintln(os.Stderr, "Opening the control plane to create a personal access token…")
	fmt.Fprintf(os.Stderr, "  %s\n", page)
	if err := openBrowser(page); err != nil {
		fmt.Fprintf(os.Stderr, "\nCouldn't auto-open browser (%v). Open the URL above manually.\n", err)
	}
	fmt.Fprintln(os.Stderr, "Create a token there, then paste it below.")
	fmt.Fprintln(os.Stderr, "(Press Enter at either prompt to create a Praxis API key instead.)")

	username := prompt("Username (your control-plane email): ", readLine)
	if username == "" {
		return false, nil
	}
	token := prompt("Personal access token: ", readSecret)
	fmt.Fprintln(os.Stderr)
	if token == "" {
		return false, nil
	}

	prof := credentials.FacetsProfile(baseURL, username, token)
	user, err := fetchAuthMe(baseURL, prof.Auth())
	if err != nil {
		verdict := "could not be verified"
		if errors.Is(err, errTokenRejected) {
			verdict = "was not accepted"
		}
		fmt.Fprintf(os.Stderr,
			"The control-plane token for %s %s at %s (%v); opening browser to create a Praxis API key…\n",
			username, verdict, baseURL, err)
		return false, nil
	}
	return true, persistVerified(out, asJSON, profileName, prof, user, username, local)
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

// pickProfile decides the profile for a login that named none, on a machine
// with more than one. A bare global login keeps "default" (the documented
// convention); returns "" for that and for single-profile machines. It steps
// in for --local, and for a --url whose host is not default's control plane:
// both would otherwise commit a directory or overwrite default on a guess.
//
// On a terminal it lists the store and asks — a number, an existing name, or
// a NEW name (which then goes through the URL and PAT prompts). Without a
// terminal, or with --json, it exits 2 with the list and a -p hint.
func pickProfile(out io.Writer, asJSON, local bool, flagURL string) (string, error) {
	// A global login writes the home store, and a pin copies a home section
	// into the tree, so the choice is always among the home profiles — never
	// the tree's, which a run from inside a local tree would otherwise see.
	store, err := credentials.LoadHome()
	if err != nil {
		return "", nil
	}
	// Identical credentials under two names — [acme] and its [default] copy —
	// are one deployment, not a choice to make.
	names := credentials.Distinct(store)
	if len(names) < 2 {
		return "", nil
	}
	// A bare login means [default]; with one, or with a --url on its host,
	// there is nothing to guess. Without one every login creates it.
	if def, ok := store[credentials.DefaultProfileName]; ok && !local {
		if flagURL == "" || raptorstate.MatchesHost(flagURL, def.URL) {
			return "", nil
		}
	}
	if asJSON || !stdinIsTTY() {
		msg := fmt.Sprintf("%d profiles exist; say which one with -p", len(names))
		render.PrintError(out, asJSON, msg,
			"pass -p <name> (one of: "+strings.Join(names, ", ")+") or -p <new-name> --url <control-plane> to create one",
			exitcode.Usage)
		osExit(exitcode.Usage)
		return "", fmt.Errorf("%s", msg)
	}
	fmt.Fprintln(os.Stderr, "Profiles:")
	for i, n := range names {
		alias := ""
		if same := credentials.SameAs(store, n); len(same) > 0 {
			alias = fmt.Sprintf(" (also %q)", same)
		}
		fmt.Fprintf(os.Stderr, "  %d) %-12s %s%s\n", i+1, n, store[n].URL, alias)
	}
	verb := "use"
	if local {
		verb = "pin here"
	}
	answer := prompt(fmt.Sprintf("Profile to %s [%s] (number, name, or a new name): ", verb, credentials.DefaultProfileName), readLine)
	switch {
	case answer == "":
		return credentials.DefaultProfileName, nil
	case isIndex(answer, len(names)):
		i, _ := strconv.Atoi(answer)
		return names[i-1], nil
	}
	// A number that is not a position is a slip, not a new profile name.
	if _, nerr := strconv.Atoi(answer); nerr == nil {
		msg := fmt.Sprintf("%q is not a position in the list", answer)
		render.PrintError(out, asJSON, msg,
			fmt.Sprintf("pick a number from 1 to %d, or type a name", len(names)), exitcode.Usage)
		osExit(exitcode.Usage)
		return "", fmt.Errorf("%s", msg)
	}
	if err := credentials.ValidateProfileName(answer); err != nil {
		render.PrintError(out, asJSON, err.Error(), "pick a number from the list or a plain name", exitcode.Usage)
		osExit(exitcode.Usage)
		return "", err
	}
	return answer, nil
}

// isIndex reports whether s is a 1-based position in a list of n.
func isIndex(s string, n int) bool {
	i, err := strconv.Atoi(s)
	return err == nil && i >= 1 && i <= n
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
