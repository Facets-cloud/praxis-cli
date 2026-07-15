// Package igcheckouts is the host-local memory of where ig catalog members
// are checked out on this machine. In the praxis-MCP read posture reads run
// server-side (`praxis mcp ig`) and return a source path RELATIVE to a member's
// repo root; the server has no idea where — or whether — that repo lives on
// disk. This file is the record that closes the gap: git-remote → local
// checkout, written by the agent (guided by the use-ig skill) and read by the
// `praxis ig hook` nudge handler. It replaces the host-`ig` workspace.yaml the
// MCP redesign dropped, without reintroducing a local ig binary.
package igcheckouts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Entry is one remembered checkout: where a catalog member resolves locally.
// Catalogs is a LIST because one repo can be a member of several catalogs (e.g.
// control-plane in both capillary-cloud and saas-cp) while sharing a single
// checkout — the agent unions each catalog it encounters into this list. Path
// resolution itself is catalog-independent; Catalogs only enriches the nudge.
type Entry struct {
	Path     string   `json:"path"`
	Member   string   `json:"member"`
	Catalogs []string `json:"catalogs"`
}

// DefaultPath is ~/.praxis/ig-checkouts.json. It is GLOBAL (machine-level) even
// under `--local` folder-per-login: credentials already live at
// ~/.praxis/credentials, so ~/.praxis is the stable praxis home, and a checkout
// location is a machine fact, not a per-project one.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".praxis", "ig-checkouts.json")
}

// CanonicalGitURL normalizes a git remote URL to a scheme-less, user-less,
// .git-less, lowercased host/path so that https, ssh, and scp forms of the same
// repo collapse to one key. Returns "" for empty input. Both the memory file's
// keys and a cwd's origin remote pass through this before comparison, so the
// agent may store whatever `git remote get-url origin` prints.
func CanonicalGitURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// Drop the scheme.
	for _, p := range []string{"https://", "http://", "ssh://", "git://"} {
		if strings.HasPrefix(s, p) {
			s = s[len(p):]
			break
		}
	}
	// Drop a leading "user@" (scp form `git@host:...` or `ssh://git@host/...`),
	// but only when the "@" precedes the host — never an "@" inside the path.
	if i := strings.Index(s, "@"); i >= 0 && !strings.Contains(s[:i], "/") {
		s = s[i+1:]
	}
	// scp form uses `host:path`; convert the first ":" to "/" so it matches the
	// `host/path` that scheme URLs produce.
	s = strings.Replace(s, ":", "/", 1)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")
	return strings.ToLower(s)
}

// Load reads the memory file and returns entries keyed by CANONICAL git URL.
// A missing file is (nil, nil) — an agent that has never resolved a checkout is
// the normal cold-start case, not an error. A present-but-malformed file is
// (nil, err); the hook treats that as "no match" and stays silent.
func Load(path string) (map[string]Entry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var stored map[string]Entry
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, err
	}
	byCanon := make(map[string]Entry, len(stored))
	for k, v := range stored {
		if c := CanonicalGitURL(k); c != "" {
			byCanon[c] = v
		}
	}
	return byCanon, nil
}

// Lookup canonicalizes rawURL and returns the matching entry, if any.
func Lookup(byCanon map[string]Entry, rawURL string) (Entry, bool) {
	c := CanonicalGitURL(rawURL)
	if c == "" {
		return Entry{}, false
	}
	e, ok := byCanon[c]
	return e, ok
}

// NudgeContext is the additionalContext a hook injects when cwd matches a
// remembered checkout: it tells the agent the repo is an ig catalog member and
// to reach for the use-ig skill (reads run via `praxis mcp ig`) over grepping.
func NudgeContext(e Entry) string {
	cats := "an ig catalog"
	if len(e.Catalogs) > 0 {
		cats = "ig catalog(s) " + strings.Join(e.Catalogs, ", ")
	}
	return "This repo is the local checkout of catalog member " +
		orQuestion(e.Member) + " (" + cats + "). " +
		"For cross-service questions — who calls whom, frontend→backend handler, " +
		"blast radius, code↔infra — use the `use-ig` skill instead of grepping: " +
		"reads run server-side via `praxis mcp ig` (start with `praxis mcp ig ig_list_catalogs`). " +
		"This checkout's local path is remembered in ~/.praxis/ig-checkouts.json."
}

// orQuestion falls back to a placeholder for an empty field so the nudge never
// reads as "member  (…)".
func orQuestion(s string) string {
	if strings.TrimSpace(s) == "" {
		return "?"
	}
	return s
}

// AlreadyNudged records, and reports a prior, nudge for (session, key) under
// tmpDir so CwdChanged doesn't re-announce the same checkout as the agent moves
// around one session. A different checkout (key) in the same session re-nudges.
func AlreadyNudged(tmpDir, session, key string) bool {
	if session == "" {
		session = "nosession"
	}
	p := filepath.Join(tmpDir, "praxis-ig-hook-nudge-"+sanitize(session))
	if b, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(b)) == key {
		return true
	}
	_ = os.WriteFile(p, []byte(key), 0o644)
	return false
}

// sanitize keeps a session id safe as a filename component.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, s)
}
