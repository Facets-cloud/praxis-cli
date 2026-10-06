package skillcatalog

import "strings"

// replacedBy names the GLOBAL skills that the praxis and raptor CLIs now ship
// themselves, and the capability token that stands for the replacement. The
// same 30 names are agent-factory's CONSOLIDATED_GLOBAL_SKILLS. Only GLOBAL
// skills are replaced: an organization or personal skill with the same name
// stays.
var replacedBy = map[string]string{
	"aws-change-audit": "praxis-v1", "build-web-component": "praxis-v1",
	"cache-migrate": "praxis-v1", "cloud-operations": "praxis-v1",
	"cloud-waste-finder": "praxis-v1", "custom-agents-operations": "praxis-v1",
	"db-migrate": "praxis-v1", "duties-operations": "praxis-v1",
	"k8s-operations": "praxis-v1", "learning": "praxis-v1",
	"newrelic-operations": "praxis-v1", "secrets-migrate": "praxis-v1",
	"onboard-ig": "praxis-v1", "praxis-dag": "praxis-v1",
	"praxis-dag-runner": "praxis-v1", "slack-progress-tracker": "praxis-v1",
	"audit-facets-blueprint": "raptor-v1", "build-facets-module": "raptor-v1",
	"design-facets-module": "raptor-v1", "docs-helper": "raptor-v1",
	"facets-blueprint": "raptor-v1", "facets-ci": "raptor-v1",
	"facets-gcp-zero-change-import": "raptor-v1", "facets-notifications": "raptor-v1",
	"module-actions": "raptor-v1", "modules-repo-workflow": "raptor-v1",
	"release-debugging": "raptor-v1", "terraform-import": "raptor-v1",
	"zero-change-import": "raptor-v1", "facets-module-testing": "raptor-v1",
}

// ReplacedBy returns the capability token that replaces the GLOBAL skill name
// (bare, or with the praxis- install prefix), or "" when nothing replaces it.
func ReplacedBy(name string) string {
	if c, ok := replacedBy[name]; ok {
		return c
	}
	return replacedBy[strings.TrimPrefix(name, PraxisPrefix)]
}

// capabilities keeps only known tokens, in a stable order.
func capabilities(in []string) []string {
	var out []string
	for _, known := range []string{"praxis-v1", "raptor-v1"} {
		for _, c := range in {
			if c == known {
				out = append(out, known)
				break
			}
		}
	}
	return out
}

// dropReplaced removes the GLOBAL skills that a claimed capability replaces.
// The server does this too when it honors the request; this also covers a
// server that does not.
func dropReplaced(skills []Skill, caps []string) []Skill {
	out := make([]Skill, 0, len(skills))
	for _, s := range skills {
		c := ReplacedBy(s.Name)
		if s.Scope == "global" && c != "" && contains(caps, c) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
