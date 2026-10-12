// Package raptorstate reports which profile a BARE raptor command would use
// from here, so `praxis status` can compare it with the active praxis profile
// and tell the AI host when to prefix FACETS_PROFILE. The store itself is
// shared (credentials.LoadFacets reads the same file raptor does); this
// package only mirrors raptor's selection rules
// (raptor/pkg/config.Config.GetProfile):
//
//  1. env override — CONTROL_PLANE_URL set (FACETS_USERNAME/FACETS_TOKEN
//     optional with FACETS_HEADERS)
//  2. PRAXIS_PROFILE, then FACETS_PROFILE
//  3. the folder pin (.facets/profile), when this raptor follows pins
//     (PinSupport); an older raptor ignores the pin and falls through
//  4. the [default] section
//  5. the sole profile, when exactly one exists
//  6. none — raptor itself would error
package raptorstate

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
)

// Source labels which rule resolved the raptor profile. Mirrors the order
// documented on the package.
type Source string

const (
	SourceEnv        Source = "env"         // CONTROL_PLANE_URL override
	SourceEnvProfile Source = "env-profile" // PRAXIS_PROFILE or FACETS_PROFILE
	SourcePin        Source = "pin"         // .facets/profile
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
	// IgnoredPin is a pin in effect here that this raptor does not follow.
	IgnoredPin string
}

// lookPath is a seam over exec.LookPath so tests control "is raptor
// installed" without depending on the machine's PATH.
var lookPath = exec.LookPath

// Installed reports whether the raptor binary is on PATH. Single source of
// truth for the check, shared by Resolve (which stamps State.Installed) and by
// callers that only need this one bit (e.g. login's post-auth hint).
func Installed() bool {
	_, err := lookPath("raptor")
	return err == nil
}

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

// ResolveIn is Resolve over an explicit facets store. A global login run
// inside a local tree passes the HOME store, so it adopts raptor's home
// default rather than the tree's.
func ResolveIn(profiles map[string]credentials.Profile) State { return resolve(profiles) }

// resolve is the testable core — the store is explicit.
func resolve(profiles map[string]credentials.Profile) State {
	st := State{}
	st.Installed = Installed()

	// 1. Full env override — raptor requires only CONTROL_PLANE_URL here.
	if cpURL := os.Getenv("CONTROL_PLANE_URL"); cpURL != "" {
		st.Found = true
		st.Profile = "env"
		st.Source = SourceEnv
		st.ControlPlaneURL = cpURL
		st.Username = os.Getenv("FACETS_USERNAME")
		return st
	}

	// 2-3. The environment, then the folder pin. A name that doesn't exist is
	// still reported (raptor itself would error on it).
	name, src, ignored := selected()
	st.IgnoredPin = ignored
	if name != "" {
		st.Profile = name
		st.Source = src
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

// selected is the profile the environment or a folder pin names, if any. A pin
// this raptor does not follow is returned as ignored instead.
func selected() (string, Source, string) {
	for _, v := range []string{credentials.EnvProfile, credentials.FacetsEnvProfile} {
		if name := strings.TrimSpace(os.Getenv(v)); name != "" {
			return name, SourceEnvProfile, ""
		}
	}
	name, path, ok := credentials.FindPin()
	if !ok {
		return "", "", ""
	}
	if _, supported, installed := PinSupport(); installed && !supported {
		return "", "", path
	}
	return name, SourcePin, ""
}

// pinProbe is the probe PinSupport runs; a seam for tests.
var pinProbe = ProbePins

// ProbePins reports whether the raptor at path follows folder pins. The release
// that added pins also added `raptor profiles`, so its help exits 0; an older
// raptor rejects the unknown command. A probe, not a version compare, so a dev
// build answers for itself.
func ProbePins(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "profiles", "--help")
	cmd.Env = append(os.Environ(), "RAPTOR_NO_UPDATE_CHECK=1", "RAPTOR_NO_SKILLS_UPGRADE=1")
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd.Run() == nil
}

var (
	pinMu    sync.Mutex
	pinCache = map[string]bool{}
)

// PinSupport reports the raptor on PATH and whether it follows folder pins;
// installed is false when there is no raptor. The answer is kept per file
// version for the process, so an upgrade in the same run is probed again.
func PinSupport() (path string, supported, installed bool) {
	path, err := lookPath("raptor")
	if err != nil {
		return "", false, false
	}
	key := path
	if fi, serr := os.Stat(path); serr == nil {
		key = fmt.Sprintf("%s|%d|%d", path, fi.ModTime().UnixNano(), fi.Size())
	}
	pinMu.Lock()
	defer pinMu.Unlock()
	if s, ok := pinCache[key]; ok {
		return path, s, true
	}
	pinCache[key] = pinProbe(path)
	return path, pinCache[key], true
}

// SetPinProbeForTest replaces the pin probe and clears the cache until the
// returned func runs. Test-only.
func SetPinProbeForTest(fn func(string) bool) func() {
	pinMu.Lock()
	prev := pinProbe
	pinProbe, pinCache = fn, map[string]bool{}
	pinMu.Unlock()
	return func() {
		pinMu.Lock()
		pinProbe, pinCache = prev, map[string]bool{}
		pinMu.Unlock()
	}
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
