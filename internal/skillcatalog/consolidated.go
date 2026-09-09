package skillcatalog

// These names are the versioned wire contract, not a prefix namespace. Only
// globals with an exact name match are replaced; unknown scope stays visible.
var replacementCapability = map[string]string{
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

// Capabilities normalizes only known wire tokens into stable protocol order.
// Callers must establish package availability before passing these tokens.
func Capabilities(in []string) []string {
	var out []string
	for _, known := range []string{"praxis-v1", "raptor-v1"} {
		for _, c := range in {
			if c == known {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

func ReplacedGlobal(name, scope string, capabilities []string) bool {
	if scope != "global" {
		return false
	}
	want, ok := replacementCapability[name]
	if !ok {
		return false
	}
	for _, c := range capabilities {
		if c == want {
			return true
		}
	}
	return false
}

// FilterConsolidated also runs client-side because old servers ignore the
// optional query parameter. Organization/personal namesakes remain untouched.
func FilterConsolidated(skills []Skill, capabilities []string) []Skill {
	out := make([]Skill, 0, len(skills))
	for _, s := range skills {
		if !ReplacedGlobal(s.Name, s.Scope, capabilities) {
			out = append(out, s)
		}
	}
	return out
}
