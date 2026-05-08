package skillinstall

import "fmt"

// dummySkills is the binary-embedded catalog. Currently only one skill,
// the "praxis" meta-skill — its content teaches the host AI how to
// drive the praxis CLI. Org skills come from the server's
// /v1/skills/bundle endpoint and are installed alongside this one
// during `praxis login`.
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

## Setup is two steps

` + "```" + `
brew install praxis    ← happens once, by the user
praxis login           ← AI runs this on first contact (or when token expires)
` + "```" + `

That's the entire setup. Login does everything: installs this
meta-skill into your AI host's skill directory, opens the user's
browser to create an API key, fetches this org's skill catalog, and
writes the MCP tool manifest snapshot to ~/.praxis/mcp-tools.json.

## First thing to do every conversation

` + "```bash" + `
praxis status --json
` + "```" + `

Returns a small JSON snapshot:

  - ` + "`profile`" + `, ` + "`profile_source`" + ` — which profile is active and where it came from
  - ` + "`url`" + ` — Praxis deployment the active profile points at
  - ` + "`logged_in`" + ` — whether there's a usable token for that profile
  - ` + "`username`" + `, ` + "`skills_installed`" + ` — context

Branch on ` + "`logged_in`" + `.

## When ` + "`logged_in: false`" + `

**Run ` + "`praxis login`" + ` yourself.** The CLI opens the user's browser; the
user clicks "Create Key" once; the CLI exits 0 with a fresh token saved,
this profile's skill catalog installed, and the MCP manifest snapshot
refreshed. Then retry the original task.

` + "```bash" + `
praxis login                                          # default profile
praxis login --url https://acme.console.facets.cloud  # different deployment
praxis login --profile bigcorp --url https://...      # named profile
` + "```" + `

Re-running login is also how you refresh stale skills or pick up new
ones the org has published. Login is idempotent.

## Switching between Praxis deployments

If the user has multiple deployments (e.g. internal support engineers),
each one is its own profile. Switch by re-running login with --profile X.
Login wipes the previous profile's org skills (praxis-* prefix) before
installing the new one's, so there's never a mixed state on disk.

` + "```bash" + `
praxis login --profile acme        # active profile becomes acme
praxis login --profile bigcorp     # wipes acme skills, installs bigcorp
` + "```" + `

This meta-skill survives every switch. Only the catalog skills cycle.

## Output convention

Every AI-callable command supports ` + "`--json`" + ` and auto-emits JSON when
stdout is not a terminal. **Always pass ` + "`--json`" + `** from a tool loop —
the output is stable and machine-parseable.

## Exit codes (act on these)

  - ` + "`0`" + ` ok — proceed
  - ` + "`1`" + ` generic failure — read stderr
  - ` + "`2`" + ` bad command-line args — your invocation was wrong
  - ` + "`3`" + ` auth missing/expired → run ` + "`praxis login`" + ` and retry
  - ` + "`4`" + ` no config / no profile → run ` + "`praxis login --profile <name>`" + `
  - ` + "`5`" + ` network unreachable

## The full command surface

AI-callable (always pass --json):

  - ` + "`praxis status [--refresh]`" + ` — local snapshot. ` + "`--refresh`" + ` adds a live
    /auth/me call to verify the token isn't revoked.
  - ` + "`praxis mcp`" + ` — list available MCP tools (no args) or invoke one
    (` + "`praxis mcp <mcp> <fn> --arg k=v ...`" + `). See "Discovering MCP tools"
    below.
  - ` + "`praxis refresh-skills`" + ` — re-fetch this profile's catalog and
    rewrite skill files + MCP snapshot, without re-authenticating. Use
    when the org has published new skills or after ` + "`brew upgrade praxis`" + `.
  - ` + "`praxis logout`" + ` — drop creds + org skills for active profile.
    ` + "`--all`" + ` wipes everything except this meta-skill.
  - ` + "`praxis update`" + ` — self-update binary. ` + "`--json`" + ` implies ` + "`--yes`" + `.
  - ` + "`praxis version`" + ` — build metadata.

Human-only (don't try to script these):

  - ` + "`praxis login`" + ` — opens the user's browser; you (the AI) RUN this on
    the user's behalf when status shows logged_out, but the user has to
    click "Create Key" once. Wait for exit 0 before retrying the task.

## Discovering MCP tools

The server gateway exposes tools grouped by MCP namespace
(` + "`cloud_cli`" + `, ` + "`k8s_cli`" + `, ` + "`catalog_ops`" + `, ` + "`raptor_cli`" + `, …). Each tool runs
server-side under the org's managed credentials — your laptop never
holds AWS / kube secrets.

  - **List (live)**: ` + "`praxis mcp --json`" + ` → fresh fetch of every MCP +
    function + arg shape. Best when you need accuracy.
  - **Snapshot (cached)**: ` + "`~/.praxis/mcp-tools.json`" + ` is rewritten on
    every ` + "`praxis login`" + ` and ` + "`praxis refresh-skills`" + `. Grep when you
    need tool names without going to the network.
  - **Call**: ` + "`praxis mcp <mcp> <fn> --arg k=v ... --json`" + ` (or
    ` + "`--body '<json>'`" + ` for nested args). Output is the raw MCP envelope
    (` + "`{content: [...], isError?: bool}`" + `).

Example flow:
` + "```bash" + `
praxis mcp --json | jq '.mcps.k8s_cli'         # what's in k8s_cli?
praxis mcp k8s_cli list_connected_clusters --json
praxis mcp k8s_cli run_k8s_cli \
  --arg integration_name=prod-cluster \
  --arg command='get pods -n default' --json
` + "```" + `

## Don'ts

  - **Don't** tell the user to "open a browser and paste a token" — that's
    not how it works. ` + "`praxis login`" + ` handles the browser+callback.
  - **Don't** ask the user to run praxis commands. Run them yourself.
  - **Don't** parse human-readable text output. Always use ` + "`--json`" + `.
`,
}

// ContentFor returns the SKILL.md content for the given skill name.
// Currently only the "praxis" meta-skill is binary-embedded; org
// catalog skills come from the server.
func ContentFor(name string) (string, error) {
	body, ok := dummySkills[name]
	if !ok {
		return "", fmt.Errorf(
			"unknown skill %q (only the meta-skill named \"praxis\" is binary-embedded; org skills come from the server)",
			name,
		)
	}
	return body, nil
}
