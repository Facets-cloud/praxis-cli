package skillinstall

import (
	_ "embed"
	"fmt"
	"sort"
)

// gettingStartedSkill is the pre-login GTM meta-skill (Praxis by Facets): it
// teaches a freshly-installed host what Praxis can do, where to sign up, and how
// to log in. Embedded from a .md file so the (long, backtick-heavy) copy is
// authored as plain markdown rather than an escaped Go string.
//
//go:embed getting-started.md
var gettingStartedSkill string

// The binary-embedded single-file meta-skills, resolved by metaSkillBody:
// "praxis-getting-started" (the pre-login GTM surface), "praxis" (drives the
// CLI surface) and "praxis-memory" (the org-memory recall/list/add flow). All
// are installed by `praxis login`. Org skills come from the server's
// /v1/skills/bundle endpoint and live alongside these, prefixed
// `praxis-<name>`; the IsMetaSkill exclusion on UninstallByPrefix keeps the
// prefix-shaped "praxis-memory" from being wiped during profile switches.
const (
	gettingStartedSkillName = "praxis-getting-started"
	praxisSkillName         = "praxis"
	memorySkillName         = "praxis-memory"
)

// The "praxis" body is three consts rather than one string because its middle
// third is multi-profile doctrine that only ships to a machine which actually
// has several profiles — see MultiProfileMachine.
//
// praxisSkillHead is everything before that doctrine: what every user needs,
// single- and multi-profile alike.
const praxisSkillHead = `---
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
meta-skill into your AI host's skill directory, authenticates (reusing
the control-plane token raptor already holds, else opening the control
plane's personal-access-token page for the user to paste one), fetches
this org's skill catalog, and writes the MCP tool manifest snapshot to
~/.praxis/mcp-tools.json.

## First thing to do every conversation

` + "```bash" + `
praxis status --json
` + "```" + `

Returns a small JSON snapshot:

  - ` + "`profile`" + `, ` + "`profile_source`" + ` — which profile is active and where it came from
  - ` + "`url`" + ` — Praxis deployment the active profile points at
  - ` + "`logged_in`" + ` — whether there's a usable token for that profile
  - ` + "`username`" + ` — context
  - ` + "`skills_installed`" + `, ` + "`agents_installed`" + ` — installed names only
    (deduped); add ` + "`--full`" + ` for per-harness paths, or use
    ` + "`praxis agents --json`" + ` / ` + "`praxis list-skills --json`" + `
  - ` + "`raptor`" + ` — the raptor CLI's auth state and whether it targets the
    same control plane as this praxis profile (see "Raptor profile ≠
    praxis profile" below)
  - ` + "`tools`" + ` — praxis/raptor version freshness (array of per-tool
    objects with a ` + "`stale`" + ` flag)

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
each one is its own profile. ` + "`praxis profiles`" + ` lists them; the one
marked ` + "`active`" + ` is what every other command uses.

**Switch with ` + "`praxis profiles use <name>`" + ` — you can run this yourself, no
browser.** It reuses the profile's stored token, so it only needs the user
when that token is dead. It verifies the token BEFORE changing anything and
then wipes the previous profile's org skills (praxis-* prefix) and installs
the new one's, so there's never a mixed state on disk.

` + "```bash" + `
praxis profiles --json             # who's available, who's active
praxis profiles use acme --json    # active profile becomes acme
praxis profiles use bigcorp --json # wipes acme skills, installs bigcorp
` + "```" + `

Read the result: ` + "`profile`" + ` is the new active one, ` + "`previous_profile`" + ` the
old one. If ` + "`shadowed_by_project_root`" + ` is present the switch was global but
this directory is pinned to ` + "`effective_profile`" + ` by a ` + "`.praxis`" + ` marker —
commands run here still use that profile; add ` + "`--local`" + ` to repin the tree.

Exit 3 means the stored token is dead: fall back to
` + "`praxis login --profile <name>`" + `, which needs the user to click once.
A rejected or unreachable switch changes NOTHING, so it's safe to retry.

This meta-skill survives every switch. Only the catalog skills cycle.

`

// praxisSkillMultiProfile is doctrine for a machine holding SEVERAL profiles:
// how to scope yourself instead of switching, the precedence chain, and which
// commands refuse a selection. Omitted entirely on a single-profile machine,
// where there is no profile question to answer and this text would only teach
// the host to pass `-p` at the one profile it already resolves to.
const praxisSkillMultiProfile = `### Scope yourself instead of switching

` + "`profiles use`" + ` is MACHINE-GLOBAL. It rewrites the active-profile pointer
AND replaces the installed praxis-* skills, so it changes every other shell
and agent session on this machine — including skill files a concurrent session
has already read. **If you might not be the only session running, don't
switch — scope yourself.** Two ways, both write nothing:

` + "```bash" + `
# one command
praxis -p bigcorp duty list --json
praxis -p bigcorp mcp cloud_cli run --arg cmd='aws s3 ls'

# every command for the rest of THIS session
export PRAXIS_PROFILE=bigcorp
praxis duty list --json                 # profile_source is "env"
` + "```" + `

` + "`PRAXIS_PROFILE`" + ` lives in your process environment, so another session
can't see it and can't move you — and your switch can't move them. Set it via
your shell tool's env, or ` + "`export`" + ` it if your shell state persists.

**Choose deliberately:**

| Situation | Use |
| --- | --- |
| one command, or comparing two deployments | ` + "`-p <name>`" + ` |
| this whole session works in one deployment | ` + "`PRAXIS_PROFILE=<name>`" + ` |
| the USER asked to switch and owns the machine | ` + "`profiles use <name>`" + ` |

Precedence: ` + "`-p`" + ` > ` + "`PRAXIS_PROFILE`" + ` > a repo's ` + "`.praxis`" + ` pointer >
the global pointer. The flag works before or after the command name
(` + "`praxis -p x status`" + ` == ` + "`praxis status -p x`" + `).

**Limitation worth knowing:** ` + "`-p`" + ` and ` + "`PRAXIS_PROFILE`" + ` route commands
to the right deployment, but the praxis-* SKILL FILES on disk always belong to
the globally-active profile. So cross-profile work gets the right gateway with
the active profile's skill text. If you need another org's custom skills
loaded, prefer ` + "`praxis profiles use <name> --local`" + ` in that project's
directory: it installs them project-scoped and leaves every other session
alone. A global ` + "`profiles use`" + ` also works, but it swaps the skill files
for the whole machine — so say so before doing it.

Commands that REFUSE a selection naming a DIFFERENT profile than the one they
act on, with exit 2 (nothing changed). Naming the profile they'd act on anyway
is fine — it's the no-op it looks like:

- ` + "`logout`" + ` and ` + "`refresh-skills`" + ` — refuse both ` + "`-p`" + ` and
  ` + "`PRAXIS_PROFILE`" + ` when they diverge from the active profile; they delete
  or reinstall ITS skills, so pointing them elsewhere would split the two.
- ` + "`profiles use`" + ` — refuses only a ` + "`-p`" + ` that contradicts its argument.
  With ` + "`PRAXIS_PROFILE`" + ` set it still switches, and reports
  ` + "`shadowed_by_env`" + ` plus ` + "`effective_profile`" + ` because YOUR session
  keeps using the variable.

`

// praxisSkillTail is everything after the multi-profile doctrine.
const praxisSkillTail = `## Per-directory profiles (local mode)

For users working several orgs at once (one directory per customer), a
profile can be pinned to a directory tree instead of switching global
state:

` + "```bash" + `
cd ~/work/acme
praxis login --profile acme --local     # first time: authenticate + pin
praxis profiles use acme --local        # already authenticated: just pin
praxis refresh-skills --project         # same scope, no re-auth
` + "```" + `

  - Writes a pointer at ` + "`<dir>/.praxis/config.json`" + `; discovery is
    git-style, walking up from cwd (bounded to ` + "`$HOME`" + `).
  - Skills/agents install project-scoped (` + "`<dir>/.claude/skills`" + `, …),
    so several orgs' skills coexist on one machine without wiping each
    other. Credentials always stay global in ` + "`~/.praxis/credentials`" + `.
  - ` + "`praxis status`" + ` inside the tree shows
    ` + "`profile_source: \"project\"`" + ` plus ` + "`project_root`" + `; outside
    it, the global profile applies as usual.

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

  - ` + "`praxis status [--refresh] [--full]`" + ` — local snapshot. ` + "`--refresh`" + `
    adds a live /auth/me call to verify the token isn't revoked.
    ` + "`--full`" + ` expands skills/agents to per-harness install detail.
  - ` + "`praxis mcp`" + ` — list available MCP tools (no args) or invoke one
    (` + "`praxis mcp <mcp> <fn> --arg k=v ...`" + `). See "Discovering MCP tools"
    below.
  - ` + "`praxis agents [--json]`" + ` — list every agent file the CLI has
    installed on this host (custom agents from /ai-api/custom-agents,
    prefixed ` + "`praxis-`" + `). Read-only, no network call.
  - ` + "`praxis list-skills [--json]`" + ` — list every skill file the CLI
    has installed on this host, with per-harness paths. Read-only,
    no network call.
  - ` + "`praxis profiles [--refresh]`" + ` — list every profile with its URL,
    username, and login state; ` + "`active_profile`" + ` names the one in use.
    Local-only unless ` + "`--refresh`" + ` live-verifies each token.
  - ` + "`praxis profiles use <name>`" + ` — make that profile active and re-sync
    its skills + MCP snapshot. No browser: reuses the stored token, exits 3
    if it's dead (then use ` + "`praxis login --profile <name>`" + `). ` + "`--local`" + `
    pins it to the current directory tree instead of switching globally.
  - ` + "`praxis refresh-skills`" + ` — re-fetch this profile's catalog and
    rewrite skill files + MCP snapshot, without re-authenticating. Use
    when the org has published new skills or after ` + "`brew upgrade praxis`" + `.
  - ` + "`praxis logout`" + ` — drop creds + org skills for active profile.
    ` + "`--all`" + ` wipes everything except this meta-skill.
  - ` + "`praxis profiles rename OLD NEW`" + ` / ` + "`praxis profiles rm NAME`" + ` —
    credentials-only profile management; no browser, no skill changes.
    ` + "`rm`" + ` refuses the active profile (that's ` + "`praxis logout`" + `).
  - ` + "`praxis login --dry-run`" + ` — SAFE probe: reports what login would do
    (profile, URL reachability, browser vs token reuse, skill effect) and
    changes nothing. Use before any profile switch you're unsure about.
  - ` + "`praxis update`" + ` — self-update binary. ` + "`--json`" + ` implies ` + "`--yes`" + `.
  - ` + "`praxis version`" + ` — build metadata.

Human-only (don't try to script these):

  - ` + "`praxis login`" + ` — opens the user's browser; you (the AI) RUN this on
    the user's behalf when status shows logged_out, but the user has to
    click "Create Key" once. Wait for exit 0 before retrying the task.
    (` + "`--dry-run`" + ` is the exception — it's AI-safe, see above.)

## Facets control plane = the local raptor CLI

Facets control-plane objects — **projects, resources, environments,
releases, cloud accounts** — are NOT gateway MCP tools. They are managed
by the ` + "`raptor`" + ` CLI, which runs **locally on this machine**, directly in
the shell. Never route raptor through ` + "`praxis mcp`" + ` (there is no
` + "`raptor_cli`" + ` gateway tool).

` + "```bash" + `
raptor get projects -o json                    # list Facets projects
raptor get accounts -o json                    # linked cloud accounts
raptor get releases -p <project> -e <env> -o json
` + "```" + `

Preflight — once per session, before the first raptor command:

  - **Installed?** ` + "`command -v raptor`" + ` — if missing, install it for
    the user with the ` + "`raptor.install_hint.commands`" + ` from
    ` + "`praxis status --json`" + ` (already resolved for this OS/arch; no
    sudo). See "Raptor profile ≠ praxis profile" below.
  - **Logged in?** ` + "`raptor whoami`" + ` — if it errors, RUN
    ` + "`raptor login`" + ` for the user. It opens their browser and they
    complete the sign-in; it stores a PAT in
    ` + "`~/.facets/credentials`" + `. Wait for exit 0. Never ask for a token
    in chat or write credentials yourself.
  - **Up to date?** ` + "`praxis status --json`" + ` reports ` + "`tools`" + ` as an
    ARRAY, one object per tool with its ` + "`current`" + `/` + "`latest`" + ` version
    and a ` + "`stale`" + ` flag. Find the entry whose ` + "`tool`" + ` is
    ` + "`raptor`" + ` (or ` + "`praxis`" + `); if its ` + "`stale`" + ` is true, tell
    the user and offer to run ` + "`raptor upgrade`" + ` — ask first, never auto-run
    it. praxis surfaces the versions; you and the user decide.

So when the user asks about projects / resources / environments /
releases / cloud accounts, reach for ` + "`raptor`" + `, not ` + "`praxis mcp`" + `.

## Raptor profile ≠ praxis profile

praxis and raptor keep SEPARATE credential stores; switching a praxis
profile never moves raptor:

  - praxis: ` + "`~/.praxis/credentials`" + `, switched by ` + "`praxis login --profile X`" + `
  - raptor: ` + "`~/.facets/credentials`" + `, selected ONLY by the
    ` + "`FACETS_PROFILE`" + ` env var (no flag, no pointer file; unset = its
    ` + "`[default]`" + ` section)

` + "`praxis status --json`" + ` cross-checks them in the ` + "`raptor`" + ` block.
Act on it:

  - ` + "`pinned: true`" + ` — this praxis profile is paired to a raptor profile
    (set via ` + "`praxis login --raptor-profile <name>`" + `). Prefix EVERY
    raptor command: ` + "`FACETS_PROFILE=<profile> raptor …`" + `. Per-command
    prefix, never ` + "`export`" + ` — each shell call starts fresh.
  - ` + "`matches_praxis_url: false`" + ` — raptor targets a different control
    plane than this praxis profile. Say which two hosts you see and ask the
    user which is intended BEFORE any raptor write;
    read-only exploration may proceed with a note.
  - ` + "`installed: false`" + ` — raptor isn't on this machine at all, so every
    control-plane command will fail. The block carries an
    ` + "`install_hint`" + `. Point the user at ` + "`install_hint.docs`" + `
    FIRST — that's raptor's own README and the maintained source of truth
    (its steps end in ` + "`sudo mv … /usr/local/bin`" + `). If the user
    can't run those, or asks you to do it, use
    ` + "`install_hint.no_sudo_commands`" + `: already resolved for this
    OS/arch and free of sudo, which you can't answer a password prompt
    for. It installs to ` + "`~/.local/bin`" + `, so check that's on PATH
    afterwards. When ` + "`asset_url`" + ` is absent raptor publishes no
    build for this platform — docs only, don't improvise. Then continue
    to ` + "`raptor login`" + ` below.
  - ` + "`found: false`" + ` — raptor has no usable profile. RUN
    ` + "`raptor login`" + ` on the user's behalf, exactly as you do for
    ` + "`praxis login`" + `: it opens their browser and they complete the
    sign-in themselves. Wait for exit 0. Never ask for a token in chat,
    and never write ` + "`~/.facets/credentials`" + ` yourself.
  - ` + "`setup_complete`" + ` (top level, not inside the raptor block) — true
    only when praxis is logged in AND raptor is installed and resolved.
    Check it first; the two bullets above say what to do when it's false.

## Discovering MCP tools

The server gateway exposes tools grouped by MCP namespace
(` + "`cloud_cli`" + `, ` + "`k8s_cli`" + `, ` + "`catalog_ops`" + `, …). Each tool runs
server-side under the org's managed credentials — your laptop never
holds AWS / kube secrets.

  - **List (live)**: ` + "`praxis mcp --json`" + ` → fresh fetch of every MCP +
    function + arg shape. Best when you need accuracy.
  - **Snapshot (cached)**: ` + "`~/.praxis/mcp-tools.json`" + ` is rewritten on
    every ` + "`praxis login`" + ` and ` + "`praxis refresh-skills`" + `. Grep when you
    need tool names without going to the network.
  - **Call**: ` + "`praxis mcp <mcp> <fn> --arg k=v ... --json`" + ` (or
    ` + "`--body '<json>'`" + ` for nested args). Output is the tool's JSON
    result directly — the CLI unwraps the MCP envelope when the payload
    is a single JSON text item. On tool error (exit 1) or non-JSON
    payloads you get the raw envelope
    (` + "`{content: [...], isError?: bool}`" + `). Pass ` + "`--envelope`" + ` to
    always get the raw envelope.

Example flow:
` + "```bash" + `
praxis mcp --json | jq '.mcps.k8s_cli'         # what's in k8s_cli?
praxis mcp k8s_cli list_connected_clusters --json
praxis mcp k8s_cli run_k8s_cli \
  --arg integration_name=prod-cluster \
  --arg command='get pods -n default' --json
` + "```" + `

## Agents

` + "`praxis login`" + ` also installs custom agent files into the supported
hosts' subagent directories:

  - Claude Code:  ` + "`~/.claude/agents/praxis-<name>.md`" + ` (via the ` + "`Task`" + ` tool)
  - Gemini CLI:   ` + "`~/.gemini/agents/praxis-<name>.md`" + ` (via ` + "`@<name>`" + ` invocation or ` + "`/agents`" + `)

Each file's frontmatter describes when to invoke it; pick based on
the user's intent.

Codex is intentionally not targeted in v1: its documented loader
path (` + "`~/.codex/agents/*.toml`" + `) matches what the renderer produces,
but its runtime did not surface the installed files in smoke
testing. The renderer keeps the TOML path; Codex enable is a
one-line flip in ` + "`supportsAgentInstall`" + ` once the loader consumes
what's documented.

Agents shell out to ` + "`praxis mcp`" + ` for any infrastructure access — same
rewrite rule as skills. No new credentials live on the laptop.

` + "`praxis agents [--json]`" + ` lists what's currently installed.

## Don'ts

  - **Don't** tell the user to "open a browser and paste a token" — that's
    not how it works. ` + "`praxis login`" + ` handles the browser+callback.
  - **Don't** ask the user to run praxis commands. Run them yourself.
  - **Don't** parse human-readable text output. Always use ` + "`--json`" + `.
`

// praxisMemorySkill drives the org-memory recall/list/add flow.
const praxisMemorySkill = `---
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
`

// MultiProfileMachine reports whether this machine holds more than one
// credentials profile, gating the multi-profile doctrine in the "praxis"
// meta-skill.
//
// The typical customer has exactly one profile and so no profile question to
// answer: for them the precedence chain, the machine-global warnings and the
// refusal table are pure context cost, and worse, they are the mechanism that
// teaches the host to start passing `-p` at the single profile it already
// resolves to.
//
// A seam, like paths.LocalModeActive: this package must not read the
// credentials store itself (its tests would then depend on the developer's real
// ~/.praxis), so cmd wires it to credentials.List and tests set it directly.
// Unwired it reports false, so the doctrine ships only once something has
// established that there really are several profiles.
var MultiProfileMachine = func() bool { return false }

// singleFileMetaSkills names every meta-skill metaSkillBody can resolve, in the
// order they were introduced. Kept beside it: a body with no name here is
// invisible to login, and a name with no body errors at install time.
var singleFileMetaSkills = []string{gettingStartedSkillName, praxisSkillName, memorySkillName}

// metaSkillBody returns the body of a single-file binary-embedded meta-skill.
// Multi-file tree meta-skills are resolved by treeSkills instead.
func metaSkillBody(name string) (string, bool) {
	switch name {
	case gettingStartedSkillName:
		return gettingStartedSkill, true
	case praxisSkillName:
		if MultiProfileMachine() {
			return praxisSkillHead + praxisSkillMultiProfile + praxisSkillTail, true
		}
		return praxisSkillHead + praxisSkillTail, true
	case memorySkillName:
		return praxisMemorySkill, true
	}
	return "", false
}

// isSingleFileMetaSkill answers the name question without building a body —
// IsMetaSkill is called once per receipt entry during a wipe.
func isSingleFileMetaSkill(name string) bool {
	for _, n := range singleFileMetaSkills {
		if n == name {
			return true
		}
	}
	return false
}

// ContentFor returns the SKILL.md content for the given skill name.
// Binary-embedded meta-skills only; org catalog skills come from the
// server's /v1/skills/bundle endpoint.
func ContentFor(name string) (string, error) {
	body, ok := metaSkillBody(name)
	if !ok {
		return "", fmt.Errorf(
			"unknown skill %q (only binary-embedded meta-skills are resolvable via ContentFor; org skills come from the server)",
			name,
		)
	}
	return body, nil
}

// IsMetaSkill reports whether `name` is a binary-embedded meta-skill —
// either a single-file skill (singleFileMetaSkills) or a multi-file tree skill
// (treeSkills). Used by UninstallByPrefix / RemoveOrphanedByPrefix to
// preserve meta-skills when wiping the "praxis-" prefix during login
// profile-switches and logout.
func IsMetaSkill(name string) bool {
	return isSingleFileMetaSkill(name) || isTreeSkill(name)
}

// MetaSkillNames returns the names of every binary-embedded meta-skill —
// single-file and tree skills — in deterministic (alphabetical) order.
// Used by login to iterate the install step; deterministic order keeps the
// install-log output stable across runs and prevents tests from being flaky
// on map iteration randomness.
func MetaSkillNames() []string {
	set := make(map[string]struct{}, len(singleFileMetaSkills))
	for _, k := range singleFileMetaSkills {
		set[k] = struct{}{}
	}
	for k := range treeSkills() {
		set[k] = struct{}{}
	}
	names := make([]string, 0, len(set))
	for k := range set {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// bootstrapSkills is the subset of meta-skills that can be installed WITHOUT a
// login — the pre-login GTM surface. They must resolve entirely from the binary
// (no network, no credentials) so `praxis init` / the cask hook / first-run can
// land them the moment praxis is installed. Every entry MUST also be a
// meta-skill (so login refreshes it and logout preserves it).
var bootstrapSkills = []string{gettingStartedSkillName}

// BootstrapSkillNames returns the no-auth-installable meta-skills, sorted.
func BootstrapSkillNames() []string {
	out := append([]string(nil), bootstrapSkills...)
	sort.Strings(out)
	return out
}
