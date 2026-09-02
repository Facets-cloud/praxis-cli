// Package raptorstate reports which profile a BARE raptor command would use
// from here, so `praxis status` can compare it with the active praxis profile
// and tell the AI host when to prefix FACETS_PROFILE. The store itself is
// shared (credentials.LoadFacets reads the same file raptor does); this
// package only mirrors raptor's selection rules
// (raptor/pkg/config.Config.GetProfile):
//
//  1. env override — CONTROL_PLANE_URL set (FACETS_USERNAME/FACETS_TOKEN
//     optional with FACETS_HEADERS)
//  2. FACETS_PROFILE env var
//  3. the [default] section
//  4. the sole profile, when exactly one exists
//  5. none — raptor itself would error
package raptorstate

import (
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
)

// Source labels which rule resolved the raptor profile. Mirrors the order
// documented on the package.
type Source string

const (
	SourceEnv        Source = "env"         // CONTROL_PLANE_URL override
	SourceEnvProfile Source = "env-profile" // FACETS_PROFILE
	SourceDefault    Source = "default"     // [default] section
	SourceSole       Source = "sole"        // the only profile in the file
)

// State is raptor's effective auth configuration as visible from praxis.
type State struct {
	// Installed reports whether the raptor binary is on PATH.
	Installed bool
	// Found reports whether a profile (or env override) resolved. When false
	// with Profile set, FACETS_PROFILE names a section the store lacks.
	Found           bool
	Profile         string
	Source          Source
	ControlPlaneURL string
	Username        string
}

// lookPath is a seam over exec.LookPath so tests control "is raptor
// installed" without depending on the machine's PATH.
var lookPath = exec.LookPath

// Resolve reports raptor's effective auth state from the working directory.
// It never fails: an unreadable or missing credentials file simply yields
// Found=false (plus whatever the env override provides).
func Resolve() State {
	profiles, err := credentials.LoadFacets()
	if err != nil {
		profiles = nil
	}
	return resolve(profiles)
}

// resolve is the testable core — the store is explicit.
func resolve(profiles map[string]credentials.Profile) State {
	st := State{}
	if _, err := lookPath("raptor"); err == nil {
		st.Installed = true
	}

	// 1. Full env override — raptor requires only CONTROL_PLANE_URL here.
	if cpURL := os.Getenv("CONTROL_PLANE_URL"); cpURL != "" {
		st.Found = true
		st.Profile = "env"
		st.Source = SourceEnv
		st.ControlPlaneURL = cpURL
		st.Username = os.Getenv("FACETS_USERNAME")
		return st
	}

	// 2. FACETS_PROFILE. A name that doesn't exist is still reported (raptor
	// itself would error on it).
	if name := os.Getenv("FACETS_PROFILE"); name != "" {
		st.Profile = name
		st.Source = SourceEnvProfile
		if p, ok := profiles[name]; ok {
			st.Found = true
			st.ControlPlaneURL = p.URL
			st.Username = p.Username
		}
		return st
	}

	// 3. [default] section.
	if p, ok := profiles[credentials.DefaultProfileName]; ok {
		st.Found = true
		st.Profile = credentials.DefaultProfileName
		st.Source = SourceDefault
		st.ControlPlaneURL = p.URL
		st.Username = p.Username
		return st
	}

	// 4. Sole profile.
	if len(profiles) == 1 {
		for name, p := range profiles {
			st.Found = true
			st.Profile = name
			st.Source = SourceSole
			st.ControlPlaneURL = p.URL
			st.Username = p.Username
		}
		return st
	}

	// 5. Nothing resolved — zero or multiple profiles without a selector.
	return st
}

// PAT returns the (username, control-plane PAT) pair stored for the named
// profile in the shared store; ok=false when the section or either value is
// missing. Deliberately a separate call rather than a State field, so the
// token can't ride along into anything that prints State (e.g. `praxis
// status`).
func PAT(profile string) (username, token string, ok bool) {
	profiles, err := credentials.LoadFacets()
	if err != nil {
		return "", "", false
	}
	p, found := profiles[profile]
	if !found || p.Username == "" || p.Token == "" {
		return "", "", false
	}
	return p.Username, p.Token, true
}

// MatchesHost reports whether two URLs point at the same host,
// case-insensitively. Used to compare the praxis profile URL with raptor's
// control_plane_url; praxis URLs are stored canonical (post-redirect), so
// host equality is the right granularity. Either side unparseable or
// hostless → false.
func MatchesHost(praxisURL, cpURL string) bool {
	a, err := url.Parse(strings.TrimSpace(praxisURL))
	if err != nil || a.Host == "" {
		return false
	}
	b, err := url.Parse(strings.TrimSpace(cpURL))
	if err != nil || b.Host == "" {
		return false
	}
	return strings.EqualFold(a.Host, b.Host)
}
