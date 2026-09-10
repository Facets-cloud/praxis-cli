package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/raptorstate"
	"github.com/Facets-cloud/praxis-cli/internal/render"
)

// patPageURL is the control-plane page that mints a personal access token — the
// same page `raptor login` opens, so one token serves both CLIs.
func patPageURL(baseURL string) string {
	return normalizeBaseURL(baseURL) + "/v2/home#personal-access-tokens"
}

// buildPATLoginURL is patPageURL plus a cli_session nonce the control-plane UI
// reads to deposit the freshly-created token straight back to the CLI — the same
// nonce handshake the Praxis API-key flow uses (browserSessionPollLogin).
// Composed from patPageURL so the page URL exists exactly once; the nonce rides
// as a query param BEFORE the fragment so it survives the SPA's hash routing.
func buildPATLoginURL(baseURL, nonce string) string {
	return strings.Replace(patPageURL(baseURL), "#", "?cli_session="+nonce+"#", 1)
}

// patDeposit is the JSON the control-plane UI deposits for a control-plane PAT.
// A PAT authenticates as Bearer + X-Facets-Username, so the session value must
// carry BOTH the token and the username — unlike a Praxis API key, which is the
// bearer string alone. The session endpoint relays this verbatim as an opaque
// plaintext_key, so carrying the username needs no server change.
type patDeposit struct {
	Username string `json:"username"`
	Token    string `json:"token"`
}

// decodePATDeposit parses the value the control-plane UI deposited. A payload
// that isn't the expected {username, token} JSON reads as "no usable PAT", so
// the caller falls through to the Praxis API-key flow rather than failing.
func decodePATDeposit(raw string) (username, token string, err error) {
	var d patDeposit
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return "", "", err
	}
	if d.Username == "" || d.Token == "" {
		return "", "", errors.New("deposit missing username or token")
	}
	return d.Username, d.Token, nil
}

// Prompt seams: tests drive the URL prompt and the Enter-to-skip gesture
// without a terminal.
var (
	stdinIsTTY = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	// readLine reads one line from stdin, one byte at a time so nothing past
	// the newline is consumed — used by promptLoginURL and the PAT tier's
	// Enter-to-skip watcher.
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

// interactivePATEligible reports whether login should use the control-plane PAT
// pickup: any control plane reachable over a safe transport. Deliberately NOT
// gated on a TTY or JSON mode — the pickup is a browser-deposit flow that reads
// no keyboard, exactly like the Praxis API-key flow (browserSessionPollLogin),
// which already runs for agents. So an agent driving `praxis login` gets a
// raptor-usable control-plane PAT, not a Praxis API key. No auth-mode probe:
// every deployment validates control-plane PATs. Shared with --dry-run so the
// report can't disagree with the chain.
func interactivePATEligible(baseURL string) bool {
	return patTransportOK(baseURL)
}

// tryInteractivePAT opens the control plane's personal-access-token page with a
// cli_session nonce and polls the same session endpoint the API-key flow uses
// until the page deposits the token the user creates there. The deposit is JSON
// ({username, token}) — a control-plane PAT needs the username for its
// X-Facets-Username header (see patDeposit). handled=false (never an error)
// sends the caller on to the Praxis API-key flow: not eligible, skipped, nothing
// deposited in time, an unexpected payload, or a PAT the server would not take.
func tryInteractivePAT(out io.Writer, asJSON bool, profileName, baseURL string, local bool) (bool, error) {
	if !interactivePATEligible(baseURL) {
		return false, nil
	}

	nonce := randomNonce()
	page := buildPATLoginURL(baseURL, nonce)
	fmt.Fprintln(os.Stderr, "Opening the control plane to create a personal access token…")
	fmt.Fprintf(os.Stderr, "  %s\n", page)
	if err := openBrowser(page); err != nil {
		fmt.Fprintf(os.Stderr, "\nCouldn't auto-open browser (%v). Open the URL above manually.\n", err)
	}
	fmt.Fprintf(os.Stderr, "Create the token there — it's picked up automatically (up to %s).\n", loginTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	defer cancel()
	// Enter = skip to the Praxis API-key flow — but only at a real terminal,
	// where a human can press it. An agent has no keyboard (and can pass --token
	// for an API key), so its stdin is left untouched and `skipped` stays nil (a
	// nil channel never fires in the select below). readLine is captured before
	// the goroutine starts: the goroutine can outlive this call (stdin has no
	// cancel), so it must not touch the package var.
	var skipped chan struct{}
	if stdinIsTTY() {
		fmt.Fprintln(os.Stderr, "(Press Enter to skip and create a Praxis API key instead.)")
		skipped = make(chan struct{})
		read := readLine
		go func() {
			if _, err := read(); err == nil {
				close(skipped)
				cancel()
			}
		}()
	}

	deposited, err := pollSessionKey(ctx, baseURL, nonce, pollInterval)
	if err != nil {
		select {
		case <-skipped:
			return patFallThrough("Skipping the control-plane token")
		default:
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return patFallThrough("No token was created within %s", loginTimeout)
		}
		return patFallThrough("Couldn't pick up a control-plane token (%v)", err)
	}

	username, token, err := decodePATDeposit(deposited)
	if err != nil {
		return patFallThrough("The control plane returned an unexpected token payload (%v)", err)
	}
	return verifyAndPersistPAT(out, asJSON, profileName, baseURL, username, token, local)
}

// verifyAndPersistPAT is the tail every interactive PAT acquisition shares:
// prove the pair against /auth/me, then persist it (credentials, raptor profile,
// post-auth setup). handled=false hands the chain on when the server won't vouch
// for the token.
func verifyAndPersistPAT(out io.Writer, asJSON bool, profileName, baseURL, username, token string, local bool) (bool, error) {
	prof := credentials.FacetsProfile(baseURL, username, token)
	user, err := fetchAuthMe(baseURL, prof.Auth())
	if err != nil {
		verdict := "could not be verified"
		if errors.Is(err, errTokenRejected) {
			verdict = "was not accepted"
		}
		return patFallThrough("The control-plane token for %s %s at %s (%v)", username, verdict, baseURL, err)
	}
	return true, persistVerified(out, asJSON, profileName, prof, user, username, local)
}

// patFallThrough reports why the PAT tier is handing off to the API-key flow.
// Stderr in both output modes: it can't corrupt --json, and a silent fallback is
// the one thing an AI host can't diagnose.
func patFallThrough(format string, args ...any) (bool, error) {
	fmt.Fprintf(os.Stderr, format+"; opening browser to create a Praxis API key…\n", args...)
	return false, nil
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
