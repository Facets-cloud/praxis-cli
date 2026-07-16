package ighook

import "testing"

func TestCanonicalGitURL(t *testing.T) {
	same := []string{
		"https://github.com/org/repo.git",
		"https://github.com/org/repo",
		"http://github.com/org/repo.git",
		"git@github.com:org/repo.git",
		"ssh://git@github.com/org/repo.git",
		"  https://github.com/ORG/Repo.git/ ",
	}
	want := "github.com/org/repo"
	for _, in := range same {
		if got := CanonicalGitURL(in); got != want {
			t.Errorf("CanonicalGitURL(%q) = %q, want %q", in, got, want)
		}
	}
	if CanonicalGitURL("") != "" {
		t.Error("empty in must give empty out")
	}
	if CanonicalGitURL("https://github.com/org/other") == want {
		t.Error("distinct repos must not collide")
	}
}

func TestNudgeContextNamesCatalogsAndSkill(t *testing.T) {
	got := NudgeContext([]string{"capillary-cloud", "saas-cp"})
	for _, sub := range []string{"capillary-cloud", "saas-cp", "use-ig", "praxis mcp ig"} {
		if !contains(got, sub) {
			t.Errorf("nudge missing %q: %s", sub, got)
		}
	}
}

func TestProcessedIsReadOnlyUntilMarked(t *testing.T) {
	tmp := t.TempDir()
	key := "github.com/org/repo"
	// Read-only: Processed must NOT record — so a failed claims call can retry.
	if Processed(tmp, "s1", key) {
		t.Error("Processed must be false before MarkProcessed")
	}
	if Processed(tmp, "s1", key) {
		t.Error("Processed must not self-record")
	}
	MarkProcessed(tmp, "s1", key)
	if !Processed(tmp, "s1", key) {
		t.Error("Processed must be true after MarkProcessed")
	}
	// Different session and different repo are independent.
	if Processed(tmp, "s2", key) {
		t.Error("a different session must be independent")
	}
	if Processed(tmp, "s1", "github.com/org/other") {
		t.Error("a different repo must be independent")
	}
}

func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
