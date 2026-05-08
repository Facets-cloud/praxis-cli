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
- **Drive Facets via Raptor** — every read-only `raptor` verb
  (projects, releases, environments, schemas, logs) through the
  `raptor_cli` MCP, under your org's Facets PAT.
- **Read & search the infrastructure catalog** — list registered
  repos, search GitHub, register newly discovered repos via the
  `catalog_ops` MCP.

Each capability is one or more functions on a server-side MCP. Run
`praxis mcp --json` for the live list of what's exposed for your org.

### Coming soon

- **Custom agents and agent management** — invoke org-specific
  subagents (devil's advocate, terraform planner, etc.) and manage
  their configuration from the CLI.
- **Incident operations** — open / query / attach evidence to
  incidents through the gateway.
- **GitHub operations** — first-class `github` MCP for repo
  hardening, PR queries, and dependency scans.
- **Slack / Teams outbound** — post incident summaries or release
  notes through gated, confirm-required outbound integrations.
- **Terraform** — direct terraform plan / state inspection through
  a dedicated MCP.

## Install

```bash
brew install Facets-cloud/tap/praxis
```

Or grab a release binary directly:
<https://github.com/Facets-cloud/praxis-cli/releases/latest>

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
2. Opens your browser to **create an API key** (you click "Create"
   once; the CLI captures the new key over a one-shot localhost
   listener).
3. **Wipes any leftover org skills** from a previous profile.
4. **Fetches your org's catalog of skills** from the Praxis server
   and installs each one as `praxis-<name>` across every AI host.
5. Writes a snapshot of available **MCP tools** to
   `~/.praxis/mcp-tools.json` so your AI can discover the gateway's
   functions without a network call.

### Where does login go?

By default `praxis login` points at the public SaaS instance
(`https://askpraxis.ai`). If your team runs Praxis at a different URL
— e.g. a Facets-hosted deployment for your org, or a self-hosted
install — pass `--url` the first time you log in. Ask your Praxis
admin if you don't know the URL.

```bash
praxis login --url https://praxis.your-org.example
```

Once saved, you don't need to pass `--url` again. Re-running
`praxis login` reuses the URL stored in your credentials file.

Re-running `praxis login` is also how you **refresh** your skills and
the MCP manifest. There's no separate `init`, `install-skill`, or
`refresh-skills` command — login is the one mutator.

That's it. Open Claude Code (or Codex, or Gemini CLI) and try:

> "Show me what's deployed in prod."
> *(your AI runs `praxis mcp ...` against your org's gateway)*
>
> "Debug my failed release."
> *(your AI loads the `praxis-release-debugging` skill that login
> just installed and walks the diagnosis with you)*

## Command surface

The CLI ships **9 user-facing commands**. All AI-callable commands
accept `--json` (auto-emit when stdout is non-TTY) with stable JSON
schemas. `login` requires a human at a browser; it still emits a
JSON envelope so your AI host can see what got installed.
`completion` is shell-script output and has no JSON form.

```text
praxis login [--profile X] [--url Y] [--token Z]
   The one-stop setup command. Idempotent. Re-run to refresh skills
   or switch profiles. The only command that's human-only — opens a
   browser.

praxis logout [--all]
   Active profile: removes credentials, all org skills (praxis-*),
   and the MCP manifest snapshot. The praxis meta-skill stays so the
   AI host can still call praxis.
   --all wipes every profile's credentials and every host's org
   skills.

praxis status [--refresh] [--json]
   Local-only snapshot of profile, auth, installed skills.
   --refresh adds a live /auth/me check (catches expired tokens).

praxis mcp [<mcp> <fn>] [--json] [--arg k=v ...] [--body '<json>']
   No args     → list every MCP namespace + function the gateway
                 exposes (with arg shapes).
   <mcp> <fn>  → invoke that function under your org credentials.

praxis refresh-skills [--json]
   Re-fetch this profile's catalog and rewrite skill files + MCP
   snapshot, without re-authenticating. Use when the org has
   published new skills or after `brew upgrade praxis`. Equivalent
   to `praxis login` minus the browser flow; requires existing
   valid credentials.

praxis update [--yes] [--json]
   Self-update binary. --json implies --yes.

praxis version [--json]   build metadata
praxis completion <shell> shell completion script (bash/zsh/fish/ps)
praxis help               cobra help
```

### Core invariant

> **Login is the canonical mutator of installed-skill state.**
> The CLI's on-disk state always matches the active profile.

Profile switching is `praxis login --profile X` — login wipes the
previous profile's org skills and installs X's. There's never a
mixed-profile state on disk. `refresh-skills` runs the same
post-login flow without changing credentials.

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

`[acme]`'s saved URL and token are still there. To switch back:

```bash
praxis login --profile acme
```

No `--url` needed — acme's URL is already saved. The token is
re-validated; if it's still fresh the browser doesn't open. The
`praxis-acme-*` skills come back from the server, `praxis-bigcorp-*`
get wiped.

### Refreshing

Same profile, no flags:

```bash
praxis login
```

Re-fetches your org's catalog and the MCP manifest snapshot. Idempotent.
Run it whenever you suspect skill content has been updated server-side
or you want to pick up new tools.

### Removing a profile

`praxis logout` removes the **active** profile's credentials, org
skills, and manifest snapshot. To remove a non-active profile, switch
to it first:

```bash
praxis login --profile acme    # make acme active
praxis logout                  # remove acme
# default and bigcorp are untouched.
```

To wipe every profile and every host:

```bash
praxis logout --all
```

## Files

```text
~/.praxis/credentials      INI, one [section] per profile (chmod 0600)
~/.praxis/config.json      active-profile pointer, set by login
~/.praxis/mcp-tools.json   manifest snapshot of gateway tools
~/.praxis/installed.json   receipt of skill files written across hosts

~/.claude/skills/praxis/SKILL.md      meta-skill (always present)
~/.claude/skills/praxis-<name>/...    org skills (cycle on profile switch)
~/.agents/skills/...                  same shape for Codex
~/.gemini/skills/...                  same shape for Gemini CLI
```

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
