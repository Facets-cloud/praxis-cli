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
	"slices"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/httpclient"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/raptorstate"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
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
	loginURL     string
	loginToken   string
	loginForce   bool
	loginLocal   bool
	loginJSON    bool
	loginTimeout time.Duration
	loginDryRun  bool
)

// interactivePATFn, noPATFn and postAuthSetup are package-level seams so tests
// can exercise login's path selection (reuse vs. PAT vs. the no-PAT terminal
// outcome) and persistence without a browser, network, or skill install.
var (
	interactivePATFn = tryInteractivePAT
	noPATFn          = noControlPlanePAT
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
	loginCmd.Flags().StringVar(&loginToken, "token", "", "save and verify an existing Praxis API key directly (no browser); a control-plane PAT comes from the browser flow, not here")
	loginCmd.Flags().BoolVar(&loginForce, "force", false, "skip the stored token and re-authenticate from the start of the chain")
	loginCmd.Flags().BoolVar(&loginLocal, "local", false,
		"pin this profile to the current folder (writes <cwd>/.facets/profile, which raptor reads too, and <cwd>/.praxis) and install its skills there; needs a control-plane PAT")
	loginCmd.Flags().BoolVar(&loginJSON, "json", false, "JSON output")
	loginCmd.Flags().DurationVar(&loginTimeout, "timeout", 90*time.Second,
		"max time to wait for the control-plane token pickup in the browser")
	loginCmd.Flags().BoolVar(&loginDryRun, "dry-run", false,
		"report what login would do (profile, URL reachability, browser-or-reuse, skill effect) and exit — no browser, no credential or skill changes")
	rootCmd.AddCommand(loginCmd)
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate, install meta-skill, and sync this profile's org catalog",
	Long: `Single entry point for setup. Login does, in order:

  1. Install the praxis meta-skill into every detected AI host
     (~/.claude/skills/praxis, plus ~/.agents/skills/praxis — the
      shared alias Codex and Gemini CLI both read) — idempotent.
  2. Authenticate with a control-plane PAT:
       a. the active profile's stored token, if still valid for this URL
          (no browser)
       b. the control-plane PAT in raptor's ~/.facets/credentials
          (no browser)
       c. the control plane's personal-access-token page — the same page
          "raptor login" opens; create a token there and login picks it
          up automatically
     Login walks the chain until one works; if none does, it stops and
     points you at the token page. Creating a new Praxis API key in the
     browser is no longer part of login. Existing Praxis API keys still
     work — reused from (a) or supplied with --token — but no new one is
     minted. Use --force to skip (a) and re-authenticate.
  3. Save credentials. praxis saves a control-plane PAT to
     ~/.facets/credentials, the store raptor reads too, in a section with
     this profile's name. An existing Praxis API key (via --token) goes to
     ~/.praxis/credentials.
  4. Re-sync the org skills where this profile is in effect: wipe the
     previous praxis-* org skills, install this profile's catalog as
     praxis-<name> across all detected AI hosts, and refresh the MCP
     manifest snapshot (mcp-tools.json).

A login never moves the machine default to another profile. The user-level
org skills follow [default], so step 4 runs globally only for a bare login
(profile "default"), or a re-login of the profile [default] holds:

  praxis login --url https://acme.console.facets.cloud   → [default], skills synced
  praxis login --profile acme --url https://...          → [acme] only

With --local, login pins the profile to this folder (<folder>/.facets/profile,
a name and no token) and installs its skills into the folder. Every praxis and
raptor command run there then uses it; other folders and sessions do not
change. In a pinned folder, --local re-logs the pinned profile.

To use a saved profile, select it — nothing is written: --profile NAME on one
command, FACETS_PROFILE=NAME for a shell, or ` + "`praxis profiles pin NAME`" + ` for a
folder. ` + "`praxis profiles set-default NAME`" + ` is the only command that changes
the machine default.

With more than one profile, --local does not guess, and a bare login does not
point [default] at another control plane on a guess: on a terminal it asks
which profile to use (a number, a name, or a new name); without a terminal it
refuses and lists the profiles.

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
		// "default" — the section a bare command of either CLI uses.
		profileName, _ := explicitProfile()
		// A --local login in a pinned folder re-logs the pinned profile.
		if pinned, _, ok := credentials.FindPin(); ok && profileName == "" && loginLocal {
			profileName = pinned
		}
		if profileName == "" {
			profileName = credentials.DefaultProfileName
			// On a multi-profile machine, --local (or a --url that is not
			// default's control plane) must not guess: ask, or refuse.
			picked, perr := pickProfile(out, asJSON, loginLocal, loginURL)
			if perr != nil {
				return perr
			}
			if picked != "" {
				profileName = picked
			}
		}

		baseURL, err := resolveLoginURL(profileName, loginURL, loginLocal)
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

		// Say what was chosen before the install output buries it.
		if !asJSON {
			scope := ""
			if loginLocal {
				if cwd, cerr := os.Getwd(); cerr == nil {
					scope = " — pinning to " + cwd
				}
			}
			fmt.Fprintf(os.Stderr, "Profile %s → %s%s\n", profileName, baseURL, scope)
		}

		// A pasted --token often carries stray whitespace; trim it so a value
		// that is only spaces reads as "no token" (falls through the chain like
		// an omitted flag) instead of a Bearer of blanks the server would reject.
		loginToken = strings.TrimSpace(loginToken)

		// --dry-run: report the plan and exit before ANY side effect —
		// no browser, no key minted, no credential write, no skill churn.
		if loginDryRun {
			return runLoginDryRun(out, asJSON, profileName, baseURL, loginLocal)
		}

		// --token is the explicit non-browser path: verify the supplied
		// key and persist it, unchanged by the reuse logic. It is always a
		// Praxis API key, which a tree's file cannot hold.
		if loginToken != "" {
			if loginLocal {
				return refuseLocalAPIKey(out, asJSON)
			}
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
		// The control-plane PAT is the only way to authenticate a new login.
		// Minting a Praxis API key was removed: existing keys keep working
		// (reuse above, or --token), but none are created here anymore.
		if handled, perr := interactivePATFn(out, asJSON, profileName, baseURL, loginLocal); handled {
			return perr
		}
		return noPATFn(out, asJSON, baseURL, loginLocal)
	},
}

// noControlPlanePAT is the terminal outcome when every PAT tier — stored token,
// raptor's ~/.facets/credentials, and the browser pickup — failed to produce a
// control-plane PAT. Login used to mint a Praxis API key here; that path is gone,
// so this points the user at the token page and exits rather than creating a key.
func noControlPlanePAT(out io.Writer, asJSON bool, baseURL string, local bool) error {
	msg := "could not obtain a control-plane personal access token"
	hint := fmt.Sprintf("create one at %s/v2/home#personal-access-tokens and re-run",
		strings.TrimRight(baseURL, "/"))
	if !local {
		// --token saves a Praxis API key, which a --local tree can't hold
		// (refuseLocalAPIKey), so only suggest it for a global login.
		hint += ", or pass --token <existing-key>"
	}
	render.PrintError(out, asJSON, msg, hint, exitcode.Auth)
	osExit(exitcode.Auth)
	return fmt.Errorf("%s", msg)
}

// refuseLocalAPIKey is the exit for a --local login that would end with a
// Praxis API key: only a control-plane PAT can live in a tree's
// .facets/credentials, and nothing has been written yet.
func refuseLocalAPIKey(out io.Writer, asJSON bool) error {
	msg := "local mode needs a control-plane PAT; this login would save a Praxis API key"
	render.PrintError(out, asJSON, msg,
		"drop --local to log in globally, or paste a control-plane PAT at the prompt (facets-mode deployments only)",
		exitcode.Usage)
	osExit(exitcode.Usage)
	return fmt.Errorf("%s", msg)
}

// resolveLoginURL resolves the URL for a NEW or EXISTING profile during
// login: explicit --url > existing profile's saved URL > the control plane
// raptor is logged in to. There is no built-in default: with none of the three
// the CLI cannot safely infer which organization deployment the user means.
//
// The store is shared with raptor, so an existing section of this name — from
// either CLI — already names the deployment. For "default" the profile raptor
// itself would select (its [default], else its sole section) is not a guess
// either: in facets mode the agent server is served under that same host. A
// NAMED profile with no section is a second tenant, not raptor's first one.
//
// A GLOBAL login reads the home store: run from inside a local tree it must
// not adopt the tree's [default] and then write it over the home one.
func resolveLoginURL(profileName, flagURL string, local bool) (string, error) {
	if flagURL != "" {
		return normalizeBaseURL(flagURL), nil
	}
	store, _ := loginStore(local)
	if p, ok := store[profileName]; ok && p.URL != "" {
		return normalizeBaseURL(p.URL), nil
	}
	if profileName == credentials.DefaultProfileName {
		if st := raptorState(local); st.Found && st.ControlPlaneURL != "" {
			return normalizeBaseURL(st.ControlPlaneURL), nil
		}
	}
	return "", fmt.Errorf("profile %q has no saved URL; pass --url to create it (or run `raptor login` first)", profileName)
}

// normalizeBaseURL strips trailing slashes so path concatenation
// (baseURL + "/v1/...") never produces a double slash, regardless of
// how the user typed --url or what an older CLI persisted.
func normalizeBaseURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// loginStore is the store a login reads: the home store for a global login,
// the store visible from the working directory for a --local one. A global
// login run inside a local tree must never reuse the tree's section and
// write it over the home [default].
func loginStore(local bool) (map[string]credentials.Profile, error) {
	if local {
		return credentials.Load()
	}
	return credentials.LoadHome()
}

// storeBefore is loginStore with a read failure read as an empty store — for
// a notice, never a decision.
func storeBefore(local bool) map[string]credentials.Profile {
	store, err := loginStore(local)
	if err != nil {
		return nil
	}
	return store
}

// raptorState is raptorstate.Resolve over the same store as loginStore.
func raptorState(local bool) raptorstate.State {
	if local {
		return raptorstate.Resolve()
	}
	profiles, err := credentials.LoadFacetsHome()
	if err != nil {
		profiles = nil
	}
	return raptorstate.ResolveIn(profiles)
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
	store, err := loginStore(local)
	if err != nil {
		return false, nil //nolint:nilerr // deliberate: can't read the store — just use the browser
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
			// The server gave a verdict: this token is dead. Fall through to
			// the control-plane PAT tiers (which may open the browser pickup, or
			// end at the token-page guidance) — no API key is minted anymore.
			if !asJSON {
				fmt.Fprintf(os.Stderr,
					"Stored token for profile %q is no longer valid (%v); trying a control-plane PAT…\n",
					profileName, err)
			}
			return false, nil // fall through to the PAT tiers
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

// tryFacetsPAT attempts a no-browser login with the control-plane PAT already
// in raptor's ~/.facets/credentials, sent as Bearer plus an X-Facets-Username
// identity header.
//
// Like tryReuseStoredToken, handled=false means the caller should fall through
// to the browser — no usable raptor profile, or the server rejected the PAT.
// The fallback uses the caller's baseURL, never the candidate's.
func tryFacetsPAT(out io.Writer, asJSON bool, profileName, baseURL string, local bool) (bool, error) {
	c, ok := facetsPATCandidate(profileName, baseURL, local)
	if !ok {
		return false, nil
	}

	prof := c.asProfile()
	user, err := fetchAuthMe(c.url, prof.Auth())
	if err != nil {
		// Only a server verdict means "this PAT is dead". A timeout or 5xx
		// leaves it unjudged, so say so rather than blaming the credential.
		// Stderr in both output modes: it can't corrupt --json, and a silent
		// fallback to the browser is the one thing an AI host can't diagnose.
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
// the one a bare raptor command would use here (FACETS_PROFILE > [default] >
// sole), so a new praxis profile can adopt the PAT raptor already holds for
// the same control plane.
//
// A candidate is only produced for an https URL that IS that control plane: in
// facets mode the agent server is served under the control-plane host itself (it
// derives its CP URL from its own X-Forwarded-Host), so a host mismatch could
// never validate — and a control-plane PAT must never be offered to a host that
// was merely typed after --url. Loopback is exempt: a developer's own agent
// server running against a remote CP.
//
// The store is the one loginStore reads: a global login adopts raptor's HOME
// default, not a tree's, so it cannot copy a tree's PAT over the home file.
func facetsPATCandidate(profileName, baseURL string, local bool) (facetsPAT, bool) {
	profiles, err := credentials.LoadFacetsHome()
	if local {
		profiles, err = credentials.LoadFacets()
	}
	if err != nil {
		return facetsPAT{}, false
	}
	st := raptorstate.ResolveIn(profiles)
	if !st.Found || st.Source == raptorstate.SourceEnv {
		return facetsPAT{}, false
	}
	// A stored PAT additionally may only travel to the control plane it came
	// from — never to a host merely typed after --url.
	if !patTransportOK(baseURL) ||
		(!isLoopbackURL(baseURL) && !raptorstate.MatchesHost(baseURL, st.ControlPlaneURL)) {
		return facetsPAT{}, false
	}
	p, found := profiles[st.Profile]
	if !found || p.Username == "" || p.Token == "" {
		return facetsPAT{}, false
	}
	return facetsPAT{section: st.Profile, username: p.Username, token: p.Token, url: baseURL}, true
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

// persistAndSetup saves the verified token under profileName and re-syncs the
// org skills where this login decides which profile is in effect. It is the
// shared tail of both the verify-then-save path (saveAndVerifyToken) and the
// reuse path (tryReuseStoredToken). Returning an error rather than os.Exit lets
// the reuse path stay non-fatal up to this point.
//
//   - local: a PAT goes to the store a read from the tree uses (a legacy tree
//     file, else the home store), the tree is pinned to profileName, and the
//     install is project-scoped. [default] is never touched.
//   - global: the profile is saved where credentials.Put routes it. A login
//     never moves [default] to another profile: only a bare login (profileName
//     is "default") or a re-login of the profile [default] copies changes it.
//     Only then are the user-level org skills re-synced; any other login saves
//     credentials and says how to use them.
//
// persistAndSetup takes the fully-built profile to save (its URL/Username/
// Token/AuthMode are authoritative — e.g. a facets profile keeps its
// control-plane username so Auth() can rebuild the X-Facets-Username header
// on reuse) and a displayName used only for the human/JSON "logged in as" line.
func persistAndSetup(out io.Writer, asJSON bool, profileName string, prof credentials.Profile, displayName string, local bool) error {
	// Say so when this login points an existing raptor section at another
	// control plane: bare raptor commands in every shell change with it.
	if prev, ok := storeBefore(local)[profileName]; ok && prev.Store == credentials.StoreFacets &&
		prev.URL != "" && !raptorstate.MatchesHost(prev.URL, prof.URL) {
		fmt.Fprintf(os.Stderr, "raptor profile %q now points at %s (was %s).\n", profileName, prof.URL, prev.URL)
	}
	if local {
		return persistLocal(out, asJSON, profileName, prof, displayName)
	}
	followsDefault, err := saveGlobal(profileName, prof)
	if err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}
	storePath, shared := storedAt(prof, "")
	if !followsDefault {
		return renderCredentialsOnly(out, asJSON, profileName, prof, displayName, storePath, shared)
	}
	restore := pinRootToHome()
	defer restore()
	state := postAuthSetup(out, asJSON, prof.URL, prof.Auth())
	recordOrg(profileName, prof.URL, state)
	if asJSON {
		payload := setupPayload(profileName, displayName, prof.URL, "", false, state)
		payload["credentials_path"] = storePath
		payload["shared_with_raptor"] = shared
		return render.JSON(out, payload)
	}
	fmt.Fprintf(out, "\n✓ Logged in as %s (profile: %s, url: %s)\n", displayName, profileName, prof.URL)
	printStoredAt(out, profileName, storePath, shared)
	return nil
}

// persistLocal is `login --local`: save the PAT where the tree reads it, pin
// the tree to profileName, and install that profile's skills project-scoped.
func persistLocal(out io.Writer, asJSON bool, profileName string, prof credentials.Profile, displayName string) error {
	// Checked before any write: raptor cannot read a Praxis API key, so a pin
	// naming one would split the two CLIs.
	if _, shared, serr := credentials.StorePath(prof, "."); serr != nil || !shared {
		return fmt.Errorf("pin profile to this directory: profile %q is a Praxis API key; local mode needs a control-plane PAT", profileName)
	}
	root, err := paths.EnsureProjectRoot()
	if err != nil {
		return fmt.Errorf("pin profile to this directory: %w", err)
	}
	storePath, err := credentials.PutFor(filepath.Dir(root), profileName, prof)
	if err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}
	projectRoot, restore, err := pinHere(profileName)
	if err != nil {
		return err
	}
	defer restore()
	state := postAuthSetup(out, asJSON, prof.URL, prof.Auth())
	recordOrg(profileName, prof.URL, state)
	// Checked after the sync, which installs or upgrades raptor.
	warn := raptorPinWarning()
	if warn != "" {
		fmt.Fprintln(os.Stderr, "Warning: "+warn)
	}
	if asJSON {
		payload := setupPayload(profileName, displayName, prof.URL, projectRoot, true, state)
		payload["credentials_path"] = storePath
		payload["shared_with_raptor"] = true
		payload["pin_path"] = credentials.PinPathIn(filepath.Dir(projectRoot))
		if warn != "" {
			payload["raptor_pin_warning"] = warn
		}
		return render.JSON(out, payload)
	}
	fmt.Fprintf(out, "\n✓ Logged in as %s and pinned profile %q to %s\n", displayName, profileName, filepath.Dir(projectRoot))
	printStoredAt(out, profileName, storePath, true)
	return nil
}

// saveGlobal writes a global login to the home store and reports whether the
// profile is the one a bare command uses (see followsDefault). A new [default]
// keeps a [default] no other section holds (credentials.PutDefault). When
// [default] is a copy of the profile, the copy is kept current.
func saveGlobal(profileName string, prof credentials.Profile) (bool, error) {
	before, err := credentials.LoadHome()
	if err != nil {
		return false, err
	}
	if profileName == credentials.DefaultProfileName {
		kept, err := credentials.PutDefault(prof)
		if err != nil {
			return false, err
		}
		if kept != "" {
			fmt.Fprintf(os.Stderr, "Kept the previous default profile as [%s].\n", kept)
		}
		return true, nil
	}
	if err := credentials.Put(profileName, prof); err != nil {
		return false, err
	}
	if slices.Contains(credentials.SameAs(before, profileName), credentials.DefaultProfileName) {
		if err := credentials.Put(credentials.DefaultProfileName, prof); err != nil {
			return false, err
		}
	}
	return followsDefault(before, profileName), nil
}

// followsDefault reports whether a global login of profileName decides what a
// bare command uses, so the user-level org skills must follow it: it IS
// [default], [default] copies it, or it is (or becomes) the only profile.
func followsDefault(home map[string]credentials.Profile, profileName string) bool {
	if profileName == credentials.DefaultProfileName ||
		slices.Contains(credentials.SameAs(home, profileName), credentials.DefaultProfileName) {
		return true
	}
	if _, ok := home[credentials.DefaultProfileName]; ok {
		return false
	}
	_, exists := home[profileName]
	return len(home) == 0 || (len(home) == 1 && exists)
}

// renderCredentialsOnly reports a login that saved credentials but left the
// machine default and its org skills alone.
func renderCredentialsOnly(out io.Writer, asJSON bool, profileName string, prof credentials.Profile,
	displayName, storePath string, shared bool) error {
	hint := fmt.Sprintf("the org skills on this machine follow [default], so they did not change. "+
		"Use this profile with --profile %[1]s, or pin a folder to it with `praxis login --profile %[1]s --local`, "+
		"or make it the machine default with `praxis profiles set-default %[1]s`", profileName)
	if asJSON {
		return render.JSON(out, map[string]any{
			"ok":                 true,
			"profile":            profileName,
			"username":           displayName,
			"url":                prof.URL,
			"scope":              scopeLabel(false),
			"skills_synced":      false,
			"credentials_path":   storePath,
			"shared_with_raptor": shared,
			"hint":               hint,
		})
	}
	fmt.Fprintf(out, "\n✓ Logged in as %s (profile: %s, url: %s)\n", displayName, profileName, prof.URL)
	printStoredAt(out, profileName, storePath, shared)
	fmt.Fprintf(out, "  Note: %s.\n", hint)
	return nil
}

// printStoredAt names the file that holds the credentials.
func printStoredAt(out io.Writer, profileName, storePath string, shared bool) {
	if shared {
		fmt.Fprintf(out, "  credentials: %s [%s] — shared with raptor\n", storePath, profileName)
		return
	}
	fmt.Fprintf(out, "  credentials: %s [%s]\n", storePath, profileName)
}

// storedAt names the file credentials.Put/PutLocal chose for prof and whether
// raptor reads it too — the store's own answer, so the report cannot disagree
// with the write. projectRoot is <cwd>/.praxis for a --local login.
func storedAt(prof credentials.Profile, projectRoot string) (string, bool) {
	dir := ""
	if projectRoot != "" {
		dir = filepath.Dir(projectRoot)
	}
	path, shared, _ := credentials.StorePath(prof, dir)
	return path, shared
}

// recordOrg notes in the active root's receipt whose org skills are installed,
// once the catalog actually came back.
func recordOrg(profileName, url string, state postAuthState) {
	if state.catalogFetched {
		_ = skillinstall.SetOrg(profileName, url)
	}
}

// pinHere pins profileName to the project tree at or above the working
// directory (<tree>/.facets/profile) and pins paths.ActiveRoot to its
// <tree>/.praxis, so the install, the receipt and the snapshot land there.
// Returns the project root and a restore func the caller MUST defer. Used by
// `login --local`, `profiles pin` and `refresh-skills --project`.
func pinHere(profileName string) (string, func(), error) {
	noop := func() {}
	root, err := paths.EnsureProjectRoot()
	if err != nil {
		return "", noop, fmt.Errorf("pin profile to this directory: %w", err)
	}
	if err := credentials.WritePin(filepath.Dir(root), profileName); err != nil {
		return "", noop, fmt.Errorf("pin profile to this directory: %w", err)
	}
	return root, paths.OverrideActiveRoot(root), nil
}

// setGlobalDefault copies profileName over the home [default] — the only
// machine-wide switch — and pins paths.ActiveRoot to home, so the re-sync that
// MUST follow installs user-level even from inside a project tree. Returns a
// restore func the caller MUST defer.
func setGlobalDefault(profileName string) (func(), error) {
	kept, err := credentials.SetDefault(profileName)
	if err != nil {
		return func() {}, fmt.Errorf("set default profile: %w", err)
	}
	if kept != "" {
		fmt.Fprintf(os.Stderr, "Kept the previous default profile as [%s].\n", kept)
	}
	return pinRootToHome(), nil
}

// pinRootToHome pins paths.ActiveRoot to the home root, so a global install
// stays user-level when run from inside a project tree.
func pinRootToHome() func() {
	home, err := paths.Dir()
	if err != nil {
		// Home is unresolvable, so there's nothing to pin to — ActiveRoot
		// will fail the same way downstream and postAuthSetup reports it.
		return func() {}
	}
	return paths.OverrideActiveRoot(home)
}

// setupPayload builds the JSON envelope for commands that make a profile
// active and re-run postAuthSetup. `praxis login` and `praxis profiles
// set-default` share it verbatim so an AI host can parse either one with the same reader.
func setupPayload(profileName, username, baseURL, projectRoot string, local bool, state postAuthState) map[string]any {
	payload := map[string]any{
		"ok":               true,
		"profile":          profileName,
		"username":         username,
		"url":              baseURL,
		"scope":            scopeLabel(local),
		"meta_skill":       state.metaSkill,
		"raptor_binary":    state.raptorBinary,
		"raptor_skills":    state.raptorSkills,
		"raptor_warning":   state.raptorWarning,
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
	req, err := http.NewRequest(http.MethodGet, baseURL+"/ai-api/auth/me", nil)
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
