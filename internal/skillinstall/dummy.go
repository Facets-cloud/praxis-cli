package skillinstall

import "fmt"

// dummySkills is the v0.1 placeholder catalog. Only one skill exists,
// named "praxis", so we can prove the multi-harness install machinery
// end-to-end. Phase 3 replaces this with a server-fetched catalog and
// the per-skill content becomes a thin pointer that calls
// `praxis skill show <name>` against the gateway.
var dummySkills = map[string]string{
	"praxis": `---
name: praxis
description: Praxis CLI is installed locally on this machine. Use when the user wants to interact with Praxis, Facets infrastructure, or asks general infra-ops questions where shelling out to ` + "`praxis`" + ` would be useful.
---

# Praxis

The Praxis CLI is installed on the user's machine.

**This is a placeholder skill for v0.1.x of the CLI.** It exists to confirm
that the multi-harness skill install machinery works end-to-end. The real
catalog (release-debugging, k8s-investigation, terraform-plan-explain,
release-status, etc.) lands in subsequent CLI releases as the Praxis
cloud gateway ships.

## What you can do today

The CLI surface is currently limited to install/version plumbing. Run
` + "`praxis --help`" + ` to see what's actually shipped — for now you can:

` + "```bash" + `
praxis version                 # show installed version
praxis update                  # self-update via GitHub Releases
praxis skill list-installed    # see what skills are installed and where
praxis logout                  # clear stored credentials
` + "```" + `

## What's coming

When the gateway ships, this same skill name will be replaced with a thin
pointer that fetches fresh content from your Praxis cloud each
invocation:

` + "```" + `
praxis skill show <name>
` + "```" + `

Until then, treat this skill as a no-op confirmation that ` + "`praxis skill install`" + `
worked across all your AI hosts.
`,
}

// ContentFor returns the SKILL.md content for the given skill name. v0.1
// only knows the hardcoded "praxis" placeholder — every other name is an
// error.
func ContentFor(name string) (string, error) {
	body, ok := dummySkills[name]
	if !ok {
		return "", fmt.Errorf(
			"unknown skill %q (v0.1 only ships the placeholder skill named \"praxis\"; the server-driven catalog lands in a later release)",
			name,
		)
	}
	return body, nil
}
