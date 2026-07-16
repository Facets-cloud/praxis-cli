package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/igcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/ighook"
	"github.com/spf13/cobra"
)

// `praxis ig hook session-start|cwd-changed` — wired into Claude Code's
// settings.json by `praxis login`. If the session's cwd is a git repo that the
// catalog server says is an ig catalog member, it injects a one-line nudge
// toward the use-ig skill. Silent + exit 0 otherwise — a hook must never block a
// session.
//
// The membership check is GENERIC and server-authoritative (`praxis ig claims`
// over cwd's git origin); it reads no agent-maintained file, so a missing or
// wrong local note can't silence it. The lookup is bounded to one successful
// check per repo per session and fails silent when offline / not logged in.

// igHookEventName maps the CLI arg to the Claude event key echoed back in the
// hook payload.
func igHookEventName(arg string) (string, error) {
	switch arg {
	case "session-start":
		return "SessionStart", nil
	case "cwd-changed":
		return "CwdChanged", nil
	}
	return "", fmt.Errorf("unknown hook %q (want session-start|cwd-changed)", arg)
}

// repoOriginURL is dir's origin remote, or "" when dir is not a git repo (or has
// no origin) — which the hook treats as "nothing to check".
func repoOriginURL(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// claimsFunc answers "which catalogs claim this canonical repo URL".
type claimsFunc func(canonURL string) ([]string, error)

// runIgHook is the pure core: given the event key, session id, tmp dir (for
// per-session dedup), the cwd's canonical git URL, and a claims lookup, it
// returns the hook JSON to print (or "" for silence). It never errors on a
// server/lookup failure — a hook must not fail a session.
func runIgHook(event, session, tmpDir, canonURL string, claims claimsFunc) (string, error) {
	if canonURL == "" {
		return "", nil // not a git repo / no origin
	}
	if ighook.Processed(tmpDir, session, canonURL) {
		return "", nil // already handled this repo this session
	}
	cats, err := claims(canonURL)
	if err != nil {
		return "", nil // offline / not logged in / timeout → silent, retry next cwd change
	}
	ighook.MarkProcessed(tmpDir, session, canonURL)
	if len(cats) == 0 {
		return "", nil // a git repo, but not a catalog member
	}
	payload := map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName":     event,
		"additionalContext": ighook.NudgeContext(cats),
	}}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// serverClaims asks the catalog server which catalogs claim canonURL, using the
// active profile's credentials. Fail-closed and fast: no auth, an unreachable
// gateway, or a slow response all return an error so the hook stays silent and a
// session start never hangs on the network.
func serverClaims(canonURL string) ([]string, error) {
	act, err := credentials.ResolveActive(igProfile)
	if err != nil {
		return nil, err
	}
	if act.Profile.Token == "" {
		return nil, errors.New("no praxis auth for hook claims check")
	}
	type res struct {
		names []string
		err   error
	}
	ch := make(chan res, 1)
	go func() {
		n, e := igcatalog.Claims(act.Profile.URL, act.Profile.Token, canonURL)
		ch <- res{n, e}
	}()
	select {
	case r := <-ch:
		return r.names, r.err
	case <-time.After(2500 * time.Millisecond):
		return nil, errors.New("claims check timed out")
	}
}

var igHookCmd = &cobra.Command{
	Use:    "hook <session-start|cwd-changed>",
	Short:  "Claude Code hook: nudge toward use-ig inside an ig catalog repo",
	Hidden: true, // wired by `praxis login`, not called by hand
	Args:   cobra.ExactArgs(1),
	// A hook's stderr must stay quiet so it never clutters a session; a bad arg
	// is a wiring bug, surfaced by the returned error only.
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		event, err := igHookEventName(args[0])
		if err != nil {
			return err
		}
		var p struct {
			Cwd       string `json:"cwd"`
			SessionID string `json:"session_id"`
		}
		if b, rErr := io.ReadAll(cmd.InOrStdin()); rErr == nil && len(b) > 0 {
			_ = json.Unmarshal(b, &p)
		}
		if p.Cwd == "" {
			p.Cwd, _ = os.Getwd()
		}
		canon := ighook.CanonicalGitURL(repoOriginURL(p.Cwd))
		out, err := runIgHook(event, p.SessionID, os.TempDir(), canon, serverClaims)
		if err != nil {
			return err
		}
		if out != "" {
			fmt.Fprintln(cmd.OutOrStdout(), out)
		}
		return nil
	},
}
