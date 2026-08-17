package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/render"
)

// runLoginDryRun reports what `praxis login` WOULD do with the given flags —
// without opening a browser, minting an API key, writing credentials, or
// touching installed skills (issue #66: login is not a safe probe).
//
// The only network traffic is a single read-only GET to /ai-api/auth/me: with
// a stored (or --token supplied) key it doubles as the token-reuse check;
// without one, an HTTP 401/403 answer still proves the deployment is
// reachable. Exit code 0 means the report is complete; exitcode.Network means
// the server could not be reached, so login's behavior can't be predicted.
func runLoginDryRun(out io.Writer, asJSON bool, profileName, baseURL string, local bool) error {
	store, _ := credentials.Load()
	prof, exists := store[profileName]

	// The profile whose org skills are on disk right now. Mirror login's
	// scope semantics: a global login resolves globally (a project pointer
	// can't redirect it); --local resolves against the full chain.
	var active credentials.Active
	if local {
		active, _ = credentials.ResolveActive("")
	} else {
		active, _ = credentials.ResolveActiveGlobal()
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
			tokenStatus, action = "stored-invalid", "browser"
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

	skillsEffect := fmt.Sprintf("org skills re-synced from %q's catalog (no profile switch)", profileName)
	if active.Name != profileName {
		skillsEffect = fmt.Sprintf("active profile switches %q → %q; %q's praxis-* org skills are wiped and %q's catalog installed",
			active.Name, profileName, active.Name, profileName)
	}

	if asJSON {
		payload := map[string]any{
			"ok":             reachable,
			"dry_run":        true,
			"profile":        profileName,
			"profile_exists": exists,
			"url":            baseURL,
			"scope":          scopeLabel(local),
			"active_profile": active.Name,
			"reachable":      reachable,
			"token_status":   tokenStatus,
			"action":         action,
			"skills_effect":  skillsEffect,
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
	}

	if !reachable {
		osExit(exitcode.Network)
	}
	return nil
}
