package skillinstall

import "fmt"

// dummySkills is the v0.x placeholder catalog. Only one skill exists,
// named "praxis", but its CONTENT teaches the host AI how to operate
// the praxis CLI correctly. When the server-driven catalog ships, this
// content gets replaced by a thin pointer that calls
// `praxis skill show <name>` against the gateway.
var dummySkills = map[string]string{
	"praxis": `---
name: praxis
description: Praxis CLI is installed locally. Use whenever the user asks about Praxis, Facets infrastructure, or wants infra/cloud/release operations done. Run praxis commands directly — don't ask the user to run them.
---

# Praxis CLI

You are the operator of the praxis CLI on this machine. The user types
intent ("debug my release", "show my AWS resources"); you shell out to
` + "`praxis`" + ` and bring the results back. The user is NOT going to type praxis
commands themselves.

## First thing to do every time praxis comes up

` + "```bash" + `
praxis status --json
` + "```" + `

This returns a small JSON snapshot:

  - ` + "`profile`" + `, ` + "`profile_source`" + ` — which profile is active and where
    that decision came from (` + "`flag`" + `, ` + "`env`" + `, ` + "`config`" + `, or ` + "`default`" + `).
  - ` + "`url`" + ` — Praxis deployment the active profile points at.
  - ` + "`logged_in`" + ` — whether there's a usable token for that profile.
  - ` + "`username`" + `, ` + "`skills_installed`" + ` — context.

Read it. Branch on ` + "`logged_in`" + `.

## When ` + "`logged_in: false`" + `

**Run ` + "`praxis login`" + ` yourself.** The CLI opens the user's browser; the
user clicks "Create" once; the CLI exits 0 with a fresh token saved.
You don't need to ask permission, paste anything, or instruct the user.
After ` + "`praxis login`" + ` returns, retry the original task.

` + "```bash" + `
praxis login                   # default profile, default URL (askpraxis.ai)
praxis login --url https://acme.console.facets.cloud   # different deployment
praxis login --profile acme --url https://acme.console.facets.cloud
                              # multi-customer support engineers
` + "```" + `

If the user has multiple Praxis deployments (e.g. internal-support
engineers), use ` + "`--profile <name>`" + `. Otherwise just ` + "`praxis login`" + `.

## Output convention

Every praxis command supports ` + "`--json`" + ` and auto-emits JSON when stdout is
not a terminal. **Always pass ` + "`--json`" + `** when you call praxis from a tool
loop — the output is stable and machine-parseable.

## Exit codes (act on these)

  - ` + "`0`" + ` ok — proceed
  - ` + "`1`" + ` generic failure — read stderr
  - ` + "`2`" + ` bad command-line args — your invocation was wrong
  - ` + "`3`" + ` auth missing/expired → run ` + "`praxis login`" + ` and retry
  - ` + "`4`" + ` no config / no profile → run ` + "`praxis login --profile <name>`" + `
  - ` + "`5`" + ` network unreachable
  - ` + "`6`" + ` no AI host detected (only relevant for skill commands)

## What you can call anytime (no auth needed)

These are local-only and safe to call freely:

  - ` + "`praxis status --json`" + ` — current state
  - ` + "`praxis list-skills`" + ` — what's installed locally
  - ` + "`praxis install-skill`" + ` / ` + "`praxis refresh-skills`" + `
  - ` + "`praxis update`" + ` — self-update CLI binary
  - ` + "`praxis version`" + ` / ` + "`praxis --help`" + ` / ` + "`praxis <cmd> --help`" + `

## What needs auth

  - ` + "`praxis whoami`" + ` — calls /ai-api/auth/me with the saved token
  - (more commands land in subsequent CLI releases — when you see a
    server-side capability in --help, expect it to require login)

## Multi-deployment users

` + "`praxis use <profile>`" + ` sets the active profile (kubectl-style). All
subsequent praxis commands use it without ` + "`--profile`" + `. Override one shot
with ` + "`--profile <name>`" + ` or ` + "`PRAXIS_PROFILE=<name>`" + ` env.

## Don'ts

  - **Don't** tell the user to "open a browser and paste a token" — that's
    obsolete. ` + "`praxis login`" + ` handles the browser+callback.
  - **Don't** ask the user to run praxis commands. Run them yourself.
  - **Don't** parse human-readable text output. Always use ` + "`--json`" + `.

## Today's state of this skill

This is the v0.x placeholder. The real catalog (release-debugging,
k8s-investigation, terraform-plan-explain, …) ships once the server
gateway lands. Until then, this skill mostly teaches you how to drive
the CLI itself.
`,
}

// ContentFor returns the SKILL.md content for the given skill name.
// v0.x only knows "praxis" — every other name is an error.
func ContentFor(name string) (string, error) {
	body, ok := dummySkills[name]
	if !ok {
		return "", fmt.Errorf(
			"unknown skill %q (v0.x only ships the placeholder skill named \"praxis\"; the server-driven catalog lands in a later release)",
			name,
		)
	}
	return body, nil
}
