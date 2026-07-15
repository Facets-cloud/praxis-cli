// Package ighook backs `praxis ig hook`, the Claude Code cwd hook that nudges
// toward the use-ig skill when the session is sitting in a repo that belongs to
// an ig catalog.
//
// The gate is deliberately GENERIC and SERVER-authoritative: given cwd's git
// origin, the hook asks the catalog server (`praxis ig claims`) whether that
// repo is a member. It does NOT read any agent-maintained file — an LLM can't be
// relied on to keep local JSON correct, so a stale/absent note must never
// silence the nudge. (The ig-checkouts.json memory the skill describes is the
// agent's own resolution scratchpad; the hook ignores it.)
package ighook

import (
	"os"
	"path/filepath"
	"strings"
)

// CanonicalGitURL normalizes a git remote URL to a scheme-less, user-less,
// .git-less, lowercased host/path so https, ssh, and scp forms of the same repo
// collapse to one identity. Returns "" for empty input.
func CanonicalGitURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	for _, p := range []string{"https://", "http://", "ssh://", "git://"} {
		if strings.HasPrefix(s, p) {
			s = s[len(p):]
			break
		}
	}
	if i := strings.Index(s, "@"); i >= 0 && !strings.Contains(s[:i], "/") {
		s = s[i+1:]
	}
	s = strings.Replace(s, ":", "/", 1)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")
	return strings.ToLower(s)
}

// NudgeContext is the additionalContext a hook injects when cwd's repo is a
// catalog member: it names the claiming catalog(s) and points the agent at the
// use-ig skill (reads run via `praxis mcp ig`) over grepping across repos.
func NudgeContext(catalogs []string) string {
	label := "an ig catalog"
	if len(catalogs) > 0 {
		label = "ig catalog(s) " + strings.Join(catalogs, ", ")
	}
	return "This repo is a member of " + label + ". For cross-service questions — " +
		"who calls whom, frontend→backend handler, blast radius, code↔infra — use the " +
		"`use-ig` skill instead of grepping across repos: reads run server-side via " +
		"`praxis mcp ig` (start with `praxis mcp ig ig_list_catalogs`)."
}

// markerPath is the per-session, per-repo sentinel used to bound the claims
// lookup to one successful check per repo per session.
func markerPath(tmpDir, session, key string) string {
	if session == "" {
		session = "nosession"
	}
	return filepath.Join(tmpDir, "praxis-ig-hook-"+sanitize(session)+"-"+sanitize(key))
}

// Processed reports whether this repo was already handled this session. It is
// READ-ONLY: it never records, so a claims call that failed (offline/no auth)
// can be retried on the next cwd change. Call MarkProcessed only after a
// successful lookup.
func Processed(tmpDir, session, key string) bool {
	_, err := os.Stat(markerPath(tmpDir, session, key))
	return err == nil
}

// MarkProcessed records that this repo was successfully checked this session, so
// CwdChanged neither re-queries the server nor re-nudges for it.
func MarkProcessed(tmpDir, session, key string) {
	_ = os.WriteFile(markerPath(tmpDir, session, key), []byte("1"), 0o644)
}

// sanitize keeps a session id / url safe as a filename component.
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
