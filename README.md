# Praxis CLI

> Bring your Praxis cloud to any local AI host (Claude Code, Codex,
> Gemini CLI). Operated by your AI; you (the human) only install the
> binary and click one button during login.

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

For a custom deployment URL or a non-default profile:

```bash
praxis login --url https://acme.console.facets.cloud
praxis login --profile bigcorp --url https://bigcorp.console.facets.cloud
```

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

## Surface (v0.7)

The CLI ships exactly **8 user-facing commands**. All AI-callable
commands accept `--json` (auto-emit when stdout is non-TTY) and have
stable JSON schemas. `login` is the only command that requires a
human at a browser; it still emits a JSON envelope at the end so
your AI host can see what got installed. `completion` is shell-script
output and has no JSON form.

```text
praxis login [--profile X] [--url Y] [--token Z]
   The one-stop setup command. Idempotent. Re-run to refresh skills
   or switch profiles. The only command that's human-only — opens a
   browser.

praxis logout [--all]
   Active profile: removes credentials, all org skills (praxis-*),
   and the MCP manifest snapshot. Meta-skill stays so the AI host
   can still call praxis.
   --all wipes every profile's credentials and every host's org
   skills.

praxis status [--refresh] [--json]
   Local-only snapshot of profile, auth, installed skills.
   --refresh adds a live /auth/me check (catches expired tokens).

praxis mcp [<mcp> <fn>] [--json] [--arg k=v ...] [--body '<json>']
   No args     → list every MCP namespace + function the gateway
                 exposes (with arg shapes).
   <mcp> <fn>  → invoke that function under your org credentials.

praxis update [--yes] [--json]
   Self-update binary. --json implies --yes.

praxis version [--json]   build metadata
praxis completion <shell> shell completion script (bash/zsh/fish/ps)
praxis help               cobra help
```

### v0.7 invariant

> **Login is the only mutator of installed-skill state.**
> The CLI's on-disk state always matches the active profile.

Profile switching is `praxis login --profile X` — login wipes the
previous profile's org skills and installs X's. There's never a
mixed-profile state on disk.

## Multiple deployments

Each Praxis deployment is its own profile. Switch by re-running
login.

```bash
praxis login                                     # → "default"
praxis login --profile acme  --url https://acme.console.facets.cloud
praxis login --profile bigcorp --url https://bigcorp.console.facets.cloud

# Active profile is now bigcorp. To switch back to acme:
praxis login --profile acme
```

Re-login with the same profile is the canonical "refresh" path.

> NOTE: `PRAXIS_PROFILE` env var and `praxis use <name>` are
> deprecated in v0.7 and removed in v0.8. Use `praxis login --profile X`.

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
