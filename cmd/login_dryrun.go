package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/render"
)

// runLoginDryRun reports what `praxis login` WOULD do with the given flags —
// without opening a browser, minting an API key, writing credentials, or
// touching installed skills (issue #66: login is not a safe probe).
//
// Network traffic is read-only GETs only: /ai-api/auth/me, plus the public
// /ai-api/auth/status probe that decides whether login would ask for a
// control-plane PAT. With
// a stored (or --token supplied) key it doubles as the token-reuse check;
// without one, an HTTP 401/403 answer still proves the deployment is
// reachable. Exit code 0 means the report is complete; exitcode.Network means
// the server could not be reached, so login's behavior can't be predicted.
func runLoginDryRun(out io.Writer, asJSON bool, profileName, baseURL string, local bool) error {
	store, _ := credentials.Load()
	prof, exists := store[profileName]

	// The profile whose org skills are on disk right now — which is a question
	// about POINTERS, not about this invocation. $PRAXIS_PROFILE changes where
	// commands route; it does not change which catalog was installed, so
	// resolving through it made the report claim "no profile switch" for a login
	// that was about to move the pointer and wipe the previous profile's skills.
	//
	// Scope still mirrors login's: a global login owns the home root, so only the
	// persisted global pointer can have put skills there; --local installs into
	// the project root, whose pointer wins when the tree is pinned.
	var activeName string
	if local {
		pinned, perr := credentials.PointerActiveName()
		if perr != nil {
			return perr
		}
		activeName = pinned
	} else {
		activeName = credentials.PersistedActiveName()
	}

	var probeAuth map[string]string
	tokenSource := "none"
	switch c, hasPAT := facetsPATCandidate(profileName, baseURL); {
	case loginToken != "":
		probeAuth, tokenSource = credentials.Profile{Token: loginToken}.Auth(), "supplied"
	case exists && prof.Token != "" && prof.URL == baseURL:
		probeAuth, tokenSource = prof.Auth(), "stored"
	case hasPAT && !loginForce:
		probeAuth, tokenSource = c.asProfile().Auth(), "facets-pat"
	}

	reachable := true
	tokenStatus, action := tokenSource, "browser"
	_, err := fetchAuthMe(baseURL, probeAuth)
	switch {
	case err == nil:
		switch tokenSource {
		case "supplied":
			tokenStatus, action = "supplied-valid", "save-token (no browser)"
		case "stored":
			if loginForce {
				tokenStatus, action = "stored-valid", "browser (--force)"
			} else {
				tokenStatus, action = "stored-valid", "reuse-token (no browser)"
			}
		case "facets-pat":
			tokenStatus, action = "facets-pat-valid", "facets-pat (no browser)"
		}
	case errors.Is(err, errTokenRejected):
		// The server answered — reachable. A 401 on an empty probe token is
		// the expected "no credentials" response, not a token verdict.
		switch tokenSource {
		case "supplied":
			tokenStatus, action = "supplied-invalid", "fail (supplied token rejected)"
		case "stored":
			// Login doesn't stop at a dead stored token: tryReuseStoredToken
			// returns handled=false and the PAT gets its turn. Probe it too, or
			// the report says "browser" where login would use the PAT.
			tokenStatus, action = "stored-invalid", "browser"
			if c, hasPAT := facetsPATCandidate(profileName, baseURL); hasPAT && !loginForce {
				if _, perr := fetchAuthMe(baseURL, c.asProfile().Auth()); perr == nil {
					tokenStatus, action = "stored-invalid, facets-pat-valid", "facets-pat (no browser)"
				}
			}
		case "facets-pat":
			tokenStatus, action = "facets-pat-invalid", "browser"
		}
	default:
		reachable = false
		if tokenSource != "none" {
			tokenStatus = tokenSource + "-unverified"
		}
		action = "unknown (server unreachable)"
	}

	// Every path that lands on the API-key browser tries the control-plane
	// sign-in first, so the report has to say so or it names the wrong page.
	if strings.HasPrefix(action, "browser") && interactivePATEligible(baseURL) {
		action = "control-plane sign-in (browser), else " + action
	}

	// A control-plane PAT is also saved as a raptor profile; an API key is not.
	raptorEffect := "unchanged (only a control-plane PAT is saved for raptor)"
	raptorTarget := fmt.Sprintf("raptor profile [%s] written to ~/.facets/credentials", raptorSection(profileName, raptorPin(profileName)))
	if local {
		raptorTarget = fmt.Sprintf("raptor profile [%s] written to <cwd>/.facets/credentials", raptorSection(profileName, raptorPin(profileName)))
	}
	switch {
	case strings.HasPrefix(action, "facets-pat"),
		strings.HasPrefix(action, "reuse-token") && prof.AuthMode == credentials.AuthModeBasic:
		raptorEffect = raptorTarget
	case strings.HasPrefix(action, "control-plane sign-in"):
		raptorEffect = raptorTarget + " once the sign-in completes"
	}

	skillsEffect := fmt.Sprintf("org skills re-synced from %q's catalog (no profile switch)", profileName)
	if activeName != profileName {
		skillsEffect = fmt.Sprintf("active profile switches %q → %q; %q's praxis-* org skills are wiped and %q's catalog installed",
			activeName, profileName, activeName, profileName)
	}

	if asJSON {
		payload := map[string]any{
			"ok":             reachable,
			"dry_run":        true,
			"profile":        profileName,
			"profile_exists": exists,
			"url":            baseURL,
			"scope":          scopeLabel(local),
			"active_profile": activeName,
			"reachable":      reachable,
			"token_status":   tokenStatus,
			"action":         action,
			"skills_effect":  skillsEffect,
			"raptor_effect":  raptorEffect,
		}
		if rerr := render.JSON(out, payload); rerr != nil {
			return rerr
		}
	} else {
		fmt.Fprintln(out, "Dry run — nothing was changed (no browser, no API key, no skill churn).")
		fmt.Fprintf(out, "  profile:  %s", profileName)
		if !exists {
			fmt.Fprint(out, " (new)")
		}
		fmt.Fprintln(out)
		fmt.Fprintf(out, "  url:      %s\n", baseURL)
		fmt.Fprintf(out, "  scope:    %s\n", scopeLabel(local))
		if reachable {
			fmt.Fprintln(out, "  server:   reachable")
		} else {
			fmt.Fprintln(out, "  server:   UNREACHABLE — login behavior can't be predicted")
		}
		fmt.Fprintf(out, "  token:    %s\n", tokenStatus)
		fmt.Fprintf(out, "  action:   %s\n", action)
		fmt.Fprintf(out, "  skills:   %s\n", skillsEffect)
		fmt.Fprintf(out, "  raptor:   %s\n", raptorEffect)
	}

	if !reachable {
		osExit(exitcode.Network)
	}
	return nil
}
