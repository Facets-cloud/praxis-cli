# Praxis CLI

> Bring your Praxis cloud to any local AI host (Claude Code, Codex,
> Gemini CLI). Operated by your AI; you (the human) only install the
> binary and click one button during login.

## What you can do

Once installed and logged in, your local AI host can:

- **Run skills published by your org** — release-debugging,
  k8s-operations, cloud-operations, terraform-import, blueprint
  management, module authoring, and any custom skills your team
  publishes. The catalog is fetched fresh on every login.
- **Investigate Kubernetes** — list connected clusters and run
  read-only kubectl against them through the `k8s_cli` MCP.
  No kubeconfig on your laptop; the server resolves credentials.
- **Query cloud infra** — run read-only `aws`, `gcloud`, and `az`
  commands against your org's integrations through the `cloud_cli`
  MCP. Mutating verbs blocked at the validator.
- **Drive Facets via Raptor** — the full `raptor` verb surface, read
  and write (projects, releases, environments, schemas, logs). Raptor
  runs as a **local CLI** under your own `raptor login` (PAT in
  `~/.facets/credentials`) — it is not a gateway MCP tool. RBAC and
  audit are enforced by the control plane server-side.
- **Read & search the infrastructure catalog** — list registered
  repos, search GitHub, register newly discovered repos via the
  `catalog_ops` MCP.

Each capability is one or more functions on a server-side MCP. Run
`praxis mcp --json` for the live list of what's exposed for your org.

- **Use org-curated agents** — custom agents (devil's advocate,
  terraform planner, release-debugger, etc.) sourced from your
  Praxis profile and installed into Claude Code's and Gemini CLI's
  subagent directories on every login. List with `praxis agents`.
  Codex has a documented loader path that matches what we render
  but its runtime didn't surface the files in smoke testing — it's
  gated off until Codex's loader catches up to its own docs.

- **Triage scheduled-agent ("duty") output** — list the org's duties,
  read a duty's recent runs, open the report artifact a run produced,
  and list a duty's findings. Read-only: pulls overnight schedule output
  into the terminal so your AI can answer "what did my duties find, and
  what should I do about it?" without opening the web UI. See
  `praxis duty --help`.

### Coming soon

- **Incident operations** — open / query / attach evidence to
  incidents through the gateway.
- **GitHub operations** — first-class `github` MCP for repo
  hardening, PR queries, and dependency scans.
- **Slack / Teams outbound** — post incident summaries or release
  notes through gated, confirm-required outbound integrations.
- **Terraform** — direct terraform plan / state inspection through
  a dedicated MCP.

## Install

**macOS** (Homebrew cask):

```bash
brew install --cask Facets-cloud/tap/praxis
```

**Linux** — download the binary directly (Homebrew on Linux does not
support casks):

```bash
curl -fsSL -o praxis \
  https://github.com/Facets-cloud/praxis-cli/releases/latest/download/praxis_linux_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
chmod +x praxis && sudo mv praxis /usr/local/bin/
```

Once installed, `praxis update` self-updates against GitHub Releases
on both platforms. Latest release: <https://github.com/Facets-cloud/praxis-cli/releases/latest>.

## Set up — one command

```bash
praxis login
```

That's literally it. `praxis login` is a single, idempotent command
that does everything you need:

1. Installs the **praxis meta-skill** into every detected AI host
   (`~/.claude/skills/praxis/`, `~/.agents/skills/praxis/`,
   `~/.gemini/skills/praxis/`). The meta-skill teaches your AI how to
   drive the rest of the CLI.
2. **Authenticates** with a control-plane token wherever it can get one:
   - If `raptor` is already logged in, login reuses that control-plane
     token (and its control plane) — nothing to click.
   - Otherwise it opens the control plane's **personal access token**
     page — the same page `raptor login` opens — and you paste the token
     you create there.
   - Failing both, it opens your browser to create a **Praxis API key**
     (you click "Create" once; the CLI polls the deployment for the new
     key and picks it up).
3. **Wipes any leftover org skills** from a previous profile.
4. **Fetches your org's catalog of skills** from the Praxis server
   and installs each one as `praxis-<name>` across every AI host.
5. Writes a snapshot of available **MCP tools** to
   `~/.praxis/mcp-tools.json` so your AI can discover the gateway's
   functions without a network call.

### Where does login go?

If `raptor` is logged in on this machine, `praxis login` targets that
control plane and authenticates with the token already there — no
`--url`, no browser. Otherwise pass your organization's control plane
URL the first time you log in; login opens its personal-access-token
page so you can create a token. Ask your Praxis administrator if you
don't know the URL.

```bash
praxis login --url https://<account-id>.console.facets.cloud
```

Once saved, you don't need to pass `--url` again. Re-running
`praxis login` reuses the URL stored in your credentials file.

Re-running `praxis login` is the canonical way to **refresh** your
skills and the MCP manifest. If you're already logged in and just want
the refresh without re-authenticating, `praxis refresh-skills` does the
same thing minus the browser flow (pass `--project` to scope the install
to the current repo instead of your user-level home dir).

That's it. Open Claude Code (or Codex, or Gemini CLI) and try:

> "Show me what's deployed in prod."
> *(your AI runs `praxis mcp ...` against your org's gateway)*
>
> "Debug my failed release."
> *(your AI loads the `praxis-release-debugging` skill that login
> just installed and walks the diagnosis with you)*

## Command surface

All AI-callable commands accept `--json` (auto-emit when stdout is
non-TTY) with stable JSON schemas. `login` is the one command that
requires a human at a browser; it still emits a JSON envelope so your
AI host can see what got installed. `completion` is shell-script
output and has no JSON form. Three further commands — `chat`, `run`
and `agent` — drive the experimental in-process coding agent and stay
off until you opt in.

One global flag and one environment variable apply everywhere:

```text
-p, --profile <name>
   Use that credentials profile for THIS invocation only. Outranks
   $PRAXIS_PROFILE and both pointers; writes nothing, so the next command
   resolves normally and the installed skills stay untouched.
   Works before or after the command name:
     praxis -p acme duty list
     praxis duty list -p acme
   `logout` and `refresh-skills` refuse it (exit 2, nothing changed) when
   it names a DIFFERENT profile than the active one — they act on
   whichever profile is active. `profiles use` refuses one that
   contradicts its argument. Naming the profile the command would act on
   anyway is allowed: it's the no-op it looks like. For `login` it names
   the profile to create or update.

PRAXIS_PROFILE=<name>
   Same thing, for every command in ONE shell or agent session. Lives in
   the process environment, so it writes nothing and no other session can
   see it — this is the concurrency-safe way to work in a profile.
     export PRAXIS_PROFILE=acme
     praxis duty list          # profile_source: env
   `logout` and `refresh-skills` refuse this too when it diverges from the
   active profile; a session already scoped to the profile they act on is
   fine. `profiles use` still works and reports `shadowed_by_env`, since
   your session keeps using the variable.
```

```text
praxis login [--profile X] [--url Y] [--token Z] [--local] [--raptor-profile R]
   The one-stop setup command. Idempotent. Re-run to refresh skills
   or switch profiles. The only command that's human-only — opens a
   browser (unless a stored token is still valid, or --token is given).
   --local pins this profile to the CURRENT directory tree (writes
   <cwd>/.praxis) and installs its skills project-scoped, instead of
   switching the global profile. See "Local mode" below.
   --raptor-profile pairs this praxis profile with a raptor profile
   (~/.facets/credentials section). See "Pairing with raptor
   profiles" below.
   --dry-run reports what login would do — resolved profile + URL,
   server reachability, browser vs stored-token reuse, and what
   happens to installed skills — then exits. No browser, no API key,
   no credential or skill changes. Exit 0 = report complete; exit 5 =
   server unreachable.

praxis profiles [--refresh] [--json]
   List every profile with URL, username, active marker, and login
   state (never prints tokens). --refresh live-verifies each token.

praxis profiles use <profile> [--local] [--json]
   Switch the active profile WITHOUT re-authenticating, and re-sync
   its skills + MCP snapshot (the same post-auth flow as login), so
   the pointer and the installed skills never disagree. Verifies the
   stored token first: exits 3 (dead token — run `praxis login
   --profile X`) or 5 (unreachable) having changed nothing.
   --local pins the profile to the CURRENT directory tree instead of
   switching globally. See "Local mode" below.

praxis profiles rename OLD NEW [--json]
   Rename a credentials section in place, keeping URL/username/token/
   raptor pairing. The global active-profile pointer follows if it
   named OLD. No browser, no new API key, no skill changes.

praxis profiles rm NAME [--json]
   Delete a NON-active profile's credentials. Refuses the active
   profile (use `praxis logout` — it also cleans up installed skills).

praxis logout [--all]
   Active profile: removes credentials, all org skills (praxis-*),
   and the MCP manifest snapshot. The praxis meta-skill stays so the
   AI host can still call praxis.
   --all wipes every profile's credentials and every host's org
   skills.

praxis status [--refresh] [--json]
   Local-only snapshot of profile, auth, installed skills. Includes a
   `raptor` block reporting which control plane the raptor CLI would
   hit (its profile resolution mirrored read-only) and whether that
   host matches the active praxis profile's URL.
   --refresh adds a live /auth/me check (catches expired tokens).

praxis mcp [<mcp> <fn>] [--json] [--arg k=v ...] [--body '<json>']
   No args     → list every MCP namespace + function the gateway
                 exposes (with arg shapes).
   <mcp> <fn>  → invoke that function under your org credentials.

praxis agents [--json]
   List every agent file praxis has installed on this host (custom
   agents from /ai-api/custom-agents, prefixed `praxis-`). Read-only,
   no network call.

praxis duty <subcommand> [--agent <name|id>] [--json]
   Query Agent Schedule ("duty") runs, findings, and the report
   artifacts they produce. Read-only. --agent defaults to the global
   "praxis" duty agent; <duty> args accept a schedule name or id.
     list                          duties under the agent
     runs --duty <d> [--limit N]   recent runs (newest first)
     run <run_id>                  one run's detail
     report <run_id>               the report artifact a run produced
     findings <duty> [--status open|resolved|all] [--limit N]

praxis refresh-skills [--project] [--json]
   Re-fetch this profile's catalog and rewrite skill files + MCP
   snapshot, without re-authenticating. Use when the org has
   published new skills or after `brew upgrade praxis`. Equivalent
   to `praxis login` minus the browser flow; requires existing
   valid credentials.
   Installs at USER level by default (~/.claude/skills, ...), so
   skills apply across every repo. When run from inside a local-mode
   directory (one with a .praxis/ root) it auto-scopes to that repo.
   Pass --project to pin the current directory to the active profile
   (like `praxis login --local`, minus auth) and install there.

praxis update [--yes] [--json]
   Self-update binary. --json implies --yes.

praxis version [--json]   build metadata
praxis completion <shell> shell completion script (bash/zsh/fish/ps)
praxis help               cobra help
```

### Experimental — the local coding agent

These run the Praxis coding agent **in-process** (no second binary) and
are disabled unless you pass `--experimental` or set
`PRAXIS_EXPERIMENTAL=1`. Provider credentials live in
`~/.praxis/agent/auth.json` — separate from the control-plane
credentials in `~/.praxis/credentials`; use `/login` inside the TUI.
`praxis version` reports the embedded agent release.

```text
praxis chat [--agents] [flags] [-- <agent flags>]
   Interactive terminal agent: multi-turn conversation, tools, MCP,
   session persistence. --agents starts on the session dashboard
   instead of a single session.

praxis run --prompt "..." [flags] [-- <agent flags>]
   One-shot headless run for scripting and CI. Emits JSON whenever
   stdout is not a TTY (--json=false for prose in a pipe).
   Output:      --json/--result-json, --usage-json, --output-schema
   Containment: --sandbox, --add-dir, --permission-rule, --safe-mode
   Budgets:     --max-turns, --max-token-budget, --max-time

praxis agent <subcommand>
   Manage the local agent runtime. (Distinct from `praxis agents`,
   which lists installed agent FILES and is not experimental.)
     plugin     plugin marketplaces and installed plugins
     mcp        the agent's own MCP servers (add/list/import/login)
     slack      Slack persona setup / status / disconnect
     sessions   persisted sessions as a table; prune empty ones
     skills     skill usage report; flags idle skills, changes nothing
     acp        serve over the Agent Client Protocol (stdio)
     sdk        serve over the JSONL SDK protocol (stdio)
```

Arguments after an `agent` subcommand — and anything after `--` on
`chat` / `run` — reach the agent runtime verbatim, so a runtime flag
newer than your praxis build is still usable:

```bash
praxis run --experimental --prompt "audit deps" -- -reflex-capture true
```

### Core invariant

> **Whatever moves the active-profile pointer also re-installs the
> skills.** The CLI's on-disk state always matches the active profile.

Only two commands move that pointer — `praxis login [--profile X]` and
`praxis profiles use X` — and both wipe the previous profile's org
skills and install X's in the same step. At the **user (global) level**
there's never a mixed-profile state on disk. `refresh-skills` runs the
same post-login flow without changing credentials or the pointer.

This is why hand-editing `~/.praxis/config.json` is not "switching": it
moves the pointer only, leaving the previous profile's skills installed.

The one deliberate exception is **local mode** (the `--local` flag):
each directory tree keeps its *own* profile and its own copy of that
profile's skills, so different repos can run different profiles at the
same time without clobbering each other. The invariant still holds
*within* each root — global state matches the global profile, and each
project root matches its own. See "Local mode" below.

## Working with multiple profiles

Each Praxis deployment you log in to is a separate **profile** stored
in `~/.praxis/credentials`. The CLI tracks an **active profile** —
that's the one your AI host operates against.

### Adding a new profile

Use `--profile <name>` to save under a name other than `default`.
The first time you log in to a profile, also pass `--url`:

```bash
praxis login                                          # → "default"
praxis login --profile acme    --url https://praxis.acme.example
praxis login --profile bigcorp --url https://praxis.bigcorp.example
```

Each `login` call **becomes the active profile** — the v0.7 model
is "logged in = active". `praxis status` will report whichever
profile you most recently logged in to.

### What happens to existing profiles

Adding a new profile **does not delete previously saved profiles**.
It only:

1. Saves the new profile's credentials in `~/.praxis/credentials`
2. Flips the active-profile pointer to the new profile
3. Wipes the *previous* profile's `praxis-*` org skills from disk
4. Installs the *new* profile's catalog skills in their place
5. Refreshes `~/.praxis/mcp-tools.json` to match

The meta-skill (`~/.claude/skills/praxis/SKILL.md`) is profile-
agnostic and never moves. Only the org skills cycle.

```text
Before login --profile bigcorp:
  ~/.praxis/credentials:  [default] [acme]      active = acme
  ~/.claude/skills:       praxis  praxis-acme-* (10)

After login --profile bigcorp --url ...:
  ~/.praxis/credentials:  [default] [acme] [bigcorp]   active = bigcorp
  ~/.claude/skills:       praxis  praxis-bigcorp-* (8)
                          (acme's skills wiped — bigcorp's installed)
```

`[acme]`'s saved URL and token are still there.

### Switching the active profile

```bash
praxis profiles              # who's available, who's active
praxis profiles use acme     # switch back to acme
```

No `--url` and no browser: acme's URL is already saved and its stored
token is re-validated against the deployment. The `praxis-acme-*` skills
come back from the server, `praxis-bigcorp-*` get wiped, and the MCP
snapshot is rewritten — the invariant above, in one command.

For a **single** command against another deployment, don't switch at all
— pass `-p`:

```bash
praxis -p bigcorp duty list     # read bigcorp, stay on acme
praxis -p bigcorp status        # profile_source: flag
```

Switching moves the installed skills; `-p` doesn't. Use `-p` for a
one-off or to compare two deployments, and `profiles use` when you
actually want to work in that deployment.

### Several sessions at once

> **`profiles use` is machine-global.** It rewrites the active-profile
> pointer *and* replaces the installed `praxis-*` skills, so it changes
> every other shell and agent session on the machine — including skill
> files a running session has already read.

If you run more than one agent session, scope each one instead of
switching:

```bash
# terminal 1 / session A
export PRAXIS_PROFILE=acme

# terminal 2 / session B
export PRAXIS_PROFILE=bigcorp
```

Neither writes anything, neither can see the other's variable, and a
`profiles use` in a third session moves neither of them. `praxis status`
reports `profile_source: env` so a session can confirm its own scope.

Run `profiles use` from a scoped session and it still switches the global
default for everyone else, but reports what your session actually uses:

```json
{ "profile": "root", "shadowed_by_env": "PRAXIS_PROFILE=vymo",
  "effective_profile": "vymo" }
```

**One limitation to be aware of:** `-p` and `PRAXIS_PROFILE` route your
commands to the right deployment, but the `praxis-*` skill files on disk
always belong to the globally-active profile — there is one user-level
skills directory. Cross-profile work therefore gets the right gateway
with the active profile's skill text. If you need another org's custom
skills loaded, either switch (affecting other sessions) or give that repo
its own copy with `profiles use <name> --local`.

Because it needs no human at a browser, this is the switch your **AI
host can run itself**. It refuses cleanly rather than half-switching:

| Situation | Exit | State |
| --- | --- | --- |
| token expired / revoked | `3` | unchanged — run `praxis login --profile acme` |
| deployment unreachable | `5` | unchanged — retry |
| no such profile | `2` | unchanged |

Switching to the profile that's already active is a valid re-sync,
equivalent to `praxis refresh-skills`.

`praxis login --profile acme` still works and does the same thing; it's
the right call when the profile is new or its token needs replacing.

### Local mode — a profile per directory

The default model is "one active profile at a time, globally." That's
ideal until you work in **multiple orgs at once** — switching profiles
globally means re-running login (and re-cycling skills) every time you
move between repos.

**Local mode** pins a profile to a directory tree, git-style. A
`.praxis/` directory in your repo marks it as a project root; any
`praxis` command run from inside that tree resolves to the pinned
profile and uses that repo's own copy of the skills. Credentials stay
shared in `~/.praxis/credentials` — local mode never duplicates secrets.

```bash
cd ~/work/acme-repo
praxis profiles use acme --local        # pins this tree to "acme"

cd ~/work/bigcorp-repo
praxis profiles use bigcorp --local     # pins this tree to "bigcorp"
```

(`praxis login --profile X --local` does the same for a profile that
isn't authenticated yet, reusing a still-valid stored token when there
is one. Re-pinning the *already-active* profile? `praxis refresh-skills
--project` is the same thing without naming it.)

Now each repo is permanently "logged in" as its own profile:

```text
~/work/acme-repo/      → profile acme,     skills in ./.claude/skills
~/work/bigcorp-repo/   → profile bigcorp,  skills in ./.claude/skills
~/  (everywhere else)  → the global profile (set by `praxis login`)
```

`praxis profiles use <name> --local` (and `praxis login --profile <name>
--local`):

1. Keeps the profile's credentials global (login saves them there
   first), then
   writes a project pointer at `<repo>/.praxis/config.json` (creating
   `<repo>/.praxis/` if needed; an existing root at or above the cwd is
   reused) — WITHOUT touching the global active-profile pointer.
2. Installs that profile's catalog skills + agents **project-scoped**
   into `<repo>/.claude/skills` (and the Codex/Gemini equivalents).
3. Writes the skill receipt and the MCP snapshot under `<repo>/.praxis/`
   too — so each repo's skill set is tracked and swapped independently.

Active-profile resolution walks this chain (first match wins):

```text
1. -p/--profile flag           ← global flag, this invocation only
2. $PRAXIS_PROFILE             ← this shell / agent session only
3. <cwd>/.praxis/config.json   ← project pointer (walks up to your home dir)
4. ~/.praxis/config.json       ← global pointer (`praxis login` / `profiles use`)
5. "default"
```

The first two write nothing, so they're safe with concurrent sessions.
A typo'd `-p`/`$PRAXIS_PROFILE` fails with exit 3 rather than falling
back — silently routing an explicit choice to another org would be worse
than stopping.

Local mode only activates when **you actually have the pinned profile**.
A `.praxis/` that's empty, or whose pointer names a profile not in your
credentials file (e.g. one a teammate committed), is **completely inert**:
resolution falls through to your global profile *and* the receipt, MCP
snapshot, and skill location stay global too. So a checked-in or
leftover `.praxis` can never lock you out of a repo, hijack you into
someone else's org, or quietly redirect your skills. `praxis status`
reports the resolved profile and, only when it actually resolved from the
project pointer, a `local mode: <repo>/.praxis` line (`profile_source:
project` in JSON).

A few things to know:

- **`login` (global), `logout`, and `profiles use` (without `--local`)
  are always global.** Run from inside a project tree they operate on
  shared credentials and user-level skills — never scoped to the repo,
  so a global switch can't overwrite a repo pinned to another profile.
  `profiles use` says so when that happens: its output carries
  `shadowed_by_project_root` and `effective_profile`, because the repo's
  pointer still wins for commands run there. Use `profiles use --local`
  / `login --local` / `refresh-skills --project` to manage local mode;
  to fully detach a repo, delete its marker: `rm -rf .praxis`.
- **Discovery is bounded to your home directory.** A repo must live
  under `$HOME` for auto-discovery to find its `.praxis/`; `--local`
  refuses to pin a directory outside it (exit 2, nothing changed).
  Symlinks are resolved before that check, so a logical home works:
  `$HOME=/tmp/x` on macOS (where `/tmp` links to `/private/tmp`) does
  contain `/private/tmp/x/repo`.
- Add `/.praxis/` to the repo's `.gitignore` — it holds a per-developer
  snapshot, not source. (If it does get committed, the inert-by-default
  behavior above keeps it harmless for teammates.)

### Refreshing

Same profile, no flags:

```bash
praxis login
```

Re-fetches your org's catalog and the MCP manifest snapshot. Idempotent.
Run it whenever you suspect skill content has been updated server-side
or you want to pick up new tools.

### Renaming a profile

```bash
praxis profiles rename test-x acme-prod
```

Credentials-only: the section keeps its URL, username, token, and
raptor pairing; the global active-profile pointer follows if it named
the old profile. No browser round-trip, no second API key, no skill
churn. (Directory trees pinned via `--local` reference profiles by
name — re-pin those with `praxis profiles use <new> --local`; until
then they harmlessly fall back to the global profile.)

### Removing a profile

For a **non-active** profile, delete just its credentials:

```bash
praxis profiles rm test-x      # credentials only; skills untouched
```

`praxis logout` removes the **active** profile's credentials, org
skills, and manifest snapshot (it refuses nothing — it's the right
tool for the active profile precisely because it cleans up skills):

```bash
praxis profiles use acme       # make acme active
praxis logout                  # remove acme fully
# default and bigcorp are untouched.
```

To wipe every profile and every host:

```bash
praxis logout --all
```

### Pairing with raptor profiles

The `raptor` CLI keeps its **own** credential store
(`~/.facets/credentials`) and selects its profile only via the
`FACETS_PROFILE` env var — switching a praxis profile never moves
raptor. For a single-profile user both default to the same control
plane and nothing needs doing. With multiple profiles on each side the
two can silently point at *different* control planes.

praxis surfaces this instead of ignoring it:

- `praxis status --json` includes a `raptor` block: raptor's resolved
  profile, its `control_plane_url`, and `matches_praxis_url` (host
  comparison against the active praxis profile). AI hosts are taught
  (via the meta-skill) to check it and to ask before writing through a
  mismatched raptor.
- `praxis login --raptor-profile <name>` stores a durable pairing on
  the praxis profile (INI key `raptor_profile`). Status then resolves
  raptor via that pin (`"pinned": true`), and AI hosts prefix every
  raptor command with `FACETS_PROFILE=<name>`. Validation is advisory:
  a pairing to a missing or host-mismatched raptor profile logs a
  warning but never fails login. Re-logins without the flag preserve
  the pairing.

praxis never *writes* `~/.facets/credentials` — the pairing is metadata
on the praxis side only. It does *read* that file: a control-plane token
raptor already holds is the first credential `praxis login` tries (step 2
above).

## Files

```text
~/.praxis/credentials      INI, one [section] per profile (chmod 0600)
                           — ALWAYS global; shared across every directory
~/.praxis/config.json      global active-profile pointer (set by `praxis login`)
~/.praxis/mcp-tools.json   manifest snapshot of gateway tools
~/.praxis/installed.json   receipt of skill files written across hosts

~/.claude/skills/praxis/SKILL.md      meta-skill (always present)
~/.claude/skills/praxis-<name>/...    org skills (cycle on profile switch)
~/.agents/skills/...                  same shape for Codex
~/.gemini/skills/...                  same shape for Gemini CLI
```

In **local mode** (`praxis login --local`), everything except credentials
moves into the repo. Credentials stay in `~/.praxis/credentials`; the
project root carries its own pointer, receipt, snapshot, and skills:

```text
<repo>/.praxis/config.json     project active-profile pointer
<repo>/.praxis/installed.json  receipt for this repo's skills
<repo>/.praxis/mcp-tools.json  this profile's MCP snapshot
<repo>/.claude/skills/praxis-<name>/...   org skills for this repo's profile
<repo>/.agents/skills/...                 same shape for Codex
<repo>/.gemini/skills/...                 same shape for Gemini CLI
```

## Security — credential-file deny rules

This repo ships a project-level `.claude/settings.json` that **denies
Claude Code from reading credential / secret files** into the
conversation transcript. The deny list covers `~/.praxis/credentials`
plus the whole of `~/.aws/`, `~/.facets/`, `~/.config/gcloud/`,
`~/.azure/`, and common key file patterns (`*.pem`, `*.key`, `id_rsa*`,
`id_ed25519*`, `*.token`). The rest of `~/.praxis/` stays readable on
purpose — the praxis skill greps `~/.praxis/mcp-tools.json`.
Project-level settings apply to anyone working inside this repo.

**Recommended for all users:** adopt the same deny rules globally in
`~/.claude/settings.json` so they apply to *every* Claude Code session
on your machine, not just sessions opened inside praxis-cli.

If `~/.claude/settings.json` **does not exist yet**, copy the entire
[`.claude/settings.json`](.claude/settings.json) from this repo as a
starting point:

```bash
mkdir -p ~/.claude
cp .claude/settings.json ~/.claude/settings.json
```

If you **already have** a `~/.claude/settings.json` with other
permissions or settings, merge in the `permissions.deny` entries
from this repo's file — don't replace your whole settings object.
Append entries to your existing `permissions.deny` array (deduping
any already present), preserving everything else.

Why this matters: the praxis CLI stores PAT tokens in
`~/.praxis/credentials`. The conversation transcript is persisted to
`~/.claude/projects/<encoded-project>/<session-uuid>.jsonl` and may
be synced, shared, or pasted. A `Read` or `cat` of a credentials
file would dump tokens into that transcript — a new exposure surface
beyond the file itself. The deny rules prevent the tool call before
it executes.

**If you find a token in a transcript:**

1. **Rotate the affected PAT immediately** via the Facets UI
   (Users → API tokens → revoke + regenerate).
2. **Clean the transcript.** Transcript files live at
   `~/.claude/projects/<encoded-project>/<session-uuid>.jsonl` (one
   JSONL file per session). Find the affected file:
   ```bash
   grep -rl '<the-leaked-token-prefix>' ~/.claude/projects/
   ```
   Then either **delete the whole session file** (simplest; loses
   conversation history) or **scrub the specific lines** containing
   the token while preserving the rest. For a quick redact:
   ```bash
   sed -i.bak 's/<token-value>/REDACTED/g' <the-file>
   ```
   After scrubbing, verify each line of the JSONL is still valid
   JSON (`python3 -c 'import sys,json; [json.loads(l) for l in sys.stdin]' < <the-file>`).

## Why a CLI

CLIs run anywhere: any AI host, CI, cron, shell pipelines. MCP
support varies by tool; `bash -c "praxis …"` doesn't. The CLI also
makes auth and audit per-invocation, so every call is attributable.

## Develop

Requirements: Go 1.21+.

```bash
git clone https://github.com/Facets-cloud/praxis-cli.git
cd praxis-cli
make build           # builds ./praxis with version stamp
make test            # go test -race ./...
make lint            # gofmt + vet + test
go test -cover ./... # coverage
```

Releases are cut by tagging `v*.*.*` and pushing — GitHub Actions
runs goreleaser, publishes the GitHub Release, and updates the Brew
tap formula automatically.

## License

MIT. See [LICENSE](LICENSE).
