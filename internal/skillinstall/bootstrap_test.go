package skillinstall

import (
	"strings"
	"testing"
)

func TestBootstrapSkillNames(t *testing.T) {
	got := BootstrapSkillNames()
	if len(got) != 1 || got[0] != "praxis-getting-started" {
		t.Fatalf("BootstrapSkillNames() = %v, want [praxis-getting-started]", got)
	}
}

func TestBootstrapSkillsAreMetaSkills(t *testing.T) {
	meta := make(map[string]bool)
	for _, n := range MetaSkillNames() {
		meta[n] = true
	}
	for _, n := range BootstrapSkillNames() {
		if !IsMetaSkill(n) {
			t.Errorf("%q must be a meta-skill (survives logout)", n)
		}
		if !meta[n] {
			t.Errorf("%q must be in MetaSkillNames() so login refreshes it", n)
		}
	}
}

func TestGettingStartedContentIsGTM(t *testing.T) {
	body, err := ContentFor("praxis-getting-started")
	if err != nil {
		t.Fatalf("ContentFor: %v", err)
	}
	// It must be resolvable purely from the binary (embedded, no network).
	if strings.TrimSpace(body) == "" {
		t.Fatal("getting-started skill body is empty (embed failed?)")
	}
	for _, sub := range []string{
		"Praxis by Facets",
		"facets.cloud/signup",
		"praxis login --url",
		"name: praxis-getting-started",
	} {
		if !strings.Contains(body, sub) {
			t.Errorf("getting-started skill missing GTM element %q", sub)
		}
	}
}
