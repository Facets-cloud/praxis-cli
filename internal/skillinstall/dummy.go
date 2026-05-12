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
	"praxis-memory": `---
name: praxis-memory
description: This Praxis deployment has a server-side memory of durable org facts (conventions, decisions, people, products, processes). Whenever the user's question may depend on org context, consult memories BEFORE answering — start with ` + "`praxis memory recall \"<terms>\" --json`" + `; if that misses or returns nothing, fall back to ` + "`praxis memory list --json`" + ` and grep the full dump yourself. Also use ` + "`praxis memory add`" + ` (after user consent) to persist a new fact the user has just shared.
---

# Praxis memories

The CLI is yours, not the user's — they will never type these
commands. You shell out via the Bash tool. Output is always JSON.

## Praxis memory vs your native auto-memory — they don't overlap

You may already have a native auto-memory directory (Claude Code
injects ` + "`# auto memory`" + ` into the system prompt pointing at
` + "`~/.claude/projects/<encoded-cwd>/memory/`" + `). **Praxis memory
does not replace it.** They are different systems for different
kinds of facts, triggered differently:

| | Native auto-memory | Praxis memory |
|---|---|---|
| Lives | locally, on this machine | server-side on the deployment |
| Scope | this user's projects on this laptop | the org's Praxis deployment (visible to other agents / colleagues per audience) |
| Belongs there | personal prefs, working context, ad-hoc observations | org conventions, decisions, people, products, processes, escalation paths |
| Trigger | YOU scoop silently when a durable fact slips by | the USER asks, or you propose and the user confirms |
| Read | auto-loaded at session start | only when you run ` + "`praxis memory recall`" + ` or ` + "`list`" + ` |

**Rules:**

1. **A personal preference goes to native auto-memory, not praxis.**
   "I prefer Python" → Write to ` + "`~/.claude/projects/<cwd>/memory/`" + `,
   NOT ` + "`praxis memory add`" + `. Praxis is for facts that travel with
   the *organization*, not the user's personal context.

2. **An organizational fact goes to praxis, not native auto-memory.**
   "We deploy on Tuesdays" / "Pravanjan owns the data pipeline" →
   ` + "`praxis memory add`" + ` (with user consent). Native auto-memory
   would silo this knowledge to one laptop.

3. **In doubt: ask the user where it should go.** "Should I remember
   this just for our chats, or save it to the org so other agents
   see it too?" — they'll tell you.

4. **Recall both when relevant.** A question that touches BOTH the
   user's personal context AND org context (e.g. "what's our usual
   deploy day, and what time zone am I in?") might want a praxis
   recall plus a glance at your native ` + "`MEMORY.md`" + `. They coexist.

## Two read paths — pick by signal strength

### 1) ` + "`praxis memory recall \"<query>\" --json`" + ` (default first move)

Server-side keyword ranking. Fast, narrow, scored. Use when the user's
question has obvious terms likely to appear in memory content.

` + "```bash" + `
praxis memory recall "retry budget for external calls" --json
# → [{slug, title, content, kind, audience, relevance_score, ...}]
` + "```" + `

Top-1 or top-2 is usually enough. Relevance score is Mongo textScore —
it ranks by keyword overlap, not semantics.

### 2) ` + "`praxis memory list --json`" + ` (fallback when keywords are weak)

Full dump of every memory the caller can see, **content included**.
Parse the JSON yourself — your own semantic judgment is stronger than
Mongo's $text. Use when:

  - ` + "`recall`" + ` returned nothing useful (zero rows, or scores all low).
  - The user's terms are vague ("when do we usually deploy?") and
    the matching memory might use very different words ("Tuesday
    release window").
  - You want to scan tags or see the breadth of what's stored.

` + "```bash" + `
praxis memory list --json | jq '.[] | select(.tags | index("infra"))'
praxis memory list --json                       # everything
praxis memory list --tag infra --json           # server-side filter
praxis memory list --limit 100 --offset 100 --json   # walk past 100
` + "```" + `

Server caps each page at 100 rows. For larger orgs walk by
` + "`--offset`" + `; in practice most orgs fit in one page.

### When NOT to consult memories

Code-only questions with no org context ("explain this Go function",
"why is this test flaky") do not warrant a recall round-trip.
Memories are about *the organization*, not generic technical help.

## Write path

When the user states an **organizational** fact likely to be useful
in future sessions — a convention, a decision, an escalation path,
who owns what — propose saving it. Get explicit consent ("save this
to org memory?") before running ` + "`add`" + `. Personal-context facts
("I prefer Python") belong in your native auto-memory, NOT here
(see "Praxis memory vs your native auto-memory" above).

` + "```bash" + `
praxis memory add \
  --title "Retry budgets" \
  --content "every external call wraps a 3-attempt exponential backoff" \
  --kind feedback \
  --audience user \
  --importance high \
  --tag infra --json
` + "```" + `

Flags:
  --title       human-readable (required)
  --content     the fact body; pass ` + "`-`" + ` to read from stdin (required)
  --kind        user | feedback | project | reference (mirrors Claude
                auto-memory taxonomy)
  --audience    user (default — the caller's cell across agents)
                | org (org-wide — every user in the org will see it)
  --importance  low | medium | high | critical
  --tag         comma-separated for filtering

Default audience=user is almost always right. Only use audience=org
when the user explicitly says "everyone should see this" or the fact
is obviously org-wide (e.g. "we deploy on Tuesdays" is org-wide;
"my IDE is VS Code" is user-only).

## Output convention

Every command emits JSON unconditionally. The ` + "`--json`" + ` flag is
accepted for praxis-skill convention compatibility but is a no-op.

## Exit codes

  - ` + "`0`" + ` ok — proceed
  - ` + "`1`" + ` generic failure (incl. unexpected HTTP errors)
  - ` + "`2`" + ` bad command-line args (e.g. missing required --title/--content)
  - ` + "`3`" + ` auth missing/expired → run ` + "`praxis login`" + ` and retry
  - ` + "`5`" + ` network unreachable

## Don'ts

  - **Don't** invent facts and persist them. Only save what the user
    actually said.
  - **Don't** call ` + "`add`" + ` without explicit user consent. Propose,
    confirm, then run.
  - **Don't** recall on every turn — only when org context is plausibly
    load-bearing for the answer.
  - **Don't** assume recall is exhaustive. If it returns nothing or
    seems off-target, ` + "`list`" + ` and grep before telling the user "I
    don't know".
  - **Don't** route personal preferences into praxis. "I prefer X"
    goes to your native auto-memory. Praxis is for facts that travel
    with the organization.
`,
}

// ContentFor returns the SKILL.md content for the given skill name.
// Binary-embedded meta-skills only; org catalog skills come from the
// server's /v1/skills/bundle endpoint.
func ContentFor(name string) (string, error) {
	body, ok := dummySkills[name]
	if !ok {
		return "", fmt.Errorf(
			"unknown skill %q (only binary-embedded meta-skills are resolvable via ContentFor; org skills come from the server)",
			name,
		)
	}
	return body, nil
}

// IsMetaSkill reports whether `name` is a binary-embedded meta-skill.
// Used by UninstallByPrefix to preserve meta-skills when wiping the
// "praxis-" prefix during login profile-switches and logout.
func IsMetaSkill(name string) bool {
	_, ok := dummySkills[name]
	return ok
}

// MetaSkillNames returns the names of every binary-embedded meta-skill.
// Used by login to iterate the install step. Order is not guaranteed.
func MetaSkillNames() []string {
	names := make([]string, 0, len(dummySkills))
	for k := range dummySkills {
		names = append(names, k)
	}
	return names
}
