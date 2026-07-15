package igcheckouts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalGitURL(t *testing.T) {
	// Every form of the SAME repo must canonicalize to one key, so an entry
	// the agent wrote as https still matches a cwd whose origin is ssh.
	same := []string{
		"https://github.com/org/repo.git",
		"https://github.com/org/repo",
		"http://github.com/org/repo.git",
		"git@github.com:org/repo.git",
		"git@github.com:org/repo",
		"ssh://git@github.com/org/repo.git",
		"  https://github.com/ORG/Repo.git/ ", // whitespace, case, trailing slash
	}
	want := "github.com/org/repo"
	for _, in := range same {
		if got := CanonicalGitURL(in); got != want {
			t.Errorf("CanonicalGitURL(%q) = %q, want %q", in, got, want)
		}
	}
	if got := CanonicalGitURL(""); got != "" {
		t.Errorf("CanonicalGitURL(\"\") = %q, want empty", got)
	}
	if got := CanonicalGitURL("   "); got != "" {
		t.Errorf("CanonicalGitURL(blank) = %q, want empty", got)
	}
	// A different repo must NOT collide.
	if CanonicalGitURL("https://github.com/org/other") == want {
		t.Error("distinct repos canonicalized to the same key")
	}
}

func writeMemory(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "ig-checkouts.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadAndLookup(t *testing.T) {
	// A repo can be a member of more than one catalog, so catalogs is a list.
	p := writeMemory(t, `{
	  "git@github.com:org/control-plane.git": {
	    "path": "/Users/me/src/control-plane",
	    "member": "control-plane",
	    "catalogs": ["capillary-cloud", "saas-cp"]
	  }
	}`)
	entries, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Stored as scp form; must be reachable via the https form (canonical match).
	e, ok := Lookup(entries, "https://github.com/org/control-plane.git")
	if !ok {
		t.Fatal("Lookup by https form did not match scp-stored entry")
	}
	if e.Path != "/Users/me/src/control-plane" || e.Member != "control-plane" {
		t.Errorf("wrong entry: %+v", e)
	}
	if len(e.Catalogs) != 2 || e.Catalogs[0] != "capillary-cloud" || e.Catalogs[1] != "saas-cp" {
		t.Errorf("catalogs not preserved as a list: %+v", e.Catalogs)
	}
	if _, ok := Lookup(entries, "https://github.com/org/unknown"); ok {
		t.Error("unknown repo unexpectedly matched")
	}
	if _, ok := Lookup(entries, ""); ok {
		t.Error("empty url unexpectedly matched")
	}
}

func TestLoadMissingFileIsEmptyNotError(t *testing.T) {
	entries, err := Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("missing file should yield no entries, got %d", len(entries))
	}
}

func TestLoadMalformedIsError(t *testing.T) {
	p := writeMemory(t, `{ this is not json`)
	if _, err := Load(p); err == nil {
		t.Error("malformed memory file should return an error so the caller can stay silent")
	}
}

func TestNudgeContextNamesMemberAndCatalog(t *testing.T) {
	e := Entry{Path: "/x", Member: "control-plane", Catalogs: []string{"capillary-cloud"}}
	got := NudgeContext(e)
	for _, sub := range []string{"control-plane", "capillary-cloud", "use-ig", "praxis mcp ig"} {
		if !contains(got, sub) {
			t.Errorf("NudgeContext missing %q; got: %s", sub, got)
		}
	}
}

func TestNudgeContextNamesAllCatalogs(t *testing.T) {
	// A repo in two catalogs: the nudge must name both so the agent knows every
	// --arg catalog= it can query.
	e := Entry{Path: "/x", Member: "control-plane", Catalogs: []string{"capillary-cloud", "saas-cp"}}
	got := NudgeContext(e)
	if !contains(got, "capillary-cloud") || !contains(got, "saas-cp") {
		t.Errorf("NudgeContext must name every catalog; got: %s", got)
	}
}

func TestNudgeContextNoCatalogsStillWorks(t *testing.T) {
	e := Entry{Path: "/x", Member: "control-plane"}
	got := NudgeContext(e)
	if !contains(got, "control-plane") || !contains(got, "use-ig") {
		t.Errorf("NudgeContext must degrade gracefully with no catalogs; got: %s", got)
	}
}

func TestAlreadyNudgedDedupsPerSession(t *testing.T) {
	tmp := t.TempDir()
	key := "github.com/org/repo"
	if AlreadyNudged(tmp, "sess-1", key) {
		t.Error("first nudge for a session must return false")
	}
	if !AlreadyNudged(tmp, "sess-1", key) {
		t.Error("second identical nudge must return true (deduped)")
	}
	// A different session is independent.
	if AlreadyNudged(tmp, "sess-2", key) {
		t.Error("a different session must not be considered already-nudged")
	}
	// A different checkout in the same session re-nudges.
	if AlreadyNudged(tmp, "sess-1", "github.com/org/other") {
		t.Error("a different checkout in the same session must re-nudge")
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
