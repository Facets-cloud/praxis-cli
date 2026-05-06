# Praxis CLI

> Bring your Praxis cloud to any local AI host (Claude Code, Codex,
> Gemini CLI). Operated by your AI; you (the human) only install the
> binary and click one button during login.

## Install

```bash
brew install Facets-cloud/tap/praxis
```

Or direct binary:

```bash
curl -fsSL https://install.askpraxis.ai | sh   # planned
# or grab a release directly:
# https://github.com/Facets-cloud/praxis-cli/releases/latest
```

## Set up — paste this prompt into your AI

Open Claude Code, Codex, Gemini CLI, or any AI host that can run
shell commands. Paste:

> I just installed the `praxis` CLI on my machine. Please set it up
> end-to-end:
>
> 1. Run `praxis init` to install the praxis skill into every AI host
>    on this machine. The skill teaches you how to use praxis.
> 2. Run `praxis login`. A browser will open; I'll click "Create Key"
>    once. You handle the rest.
> 3. Run `praxis status --json` and tell me what you see.
>
> If I have a custom Praxis deployment, use `praxis login --url <url>`.
> Otherwise the default `https://askpraxis.ai` is fine.
>
> Always pass `--json` to praxis from now on. Read exit codes — they
> tell you what to do next.

That's it. Your AI now operates praxis on your behalf. Daily use:

> "Show me what's deployed in prod."
> *(your AI runs `praxis mcp …` once those commands land)*

> "Debug my failed release."
> *(your AI sources the release-debugging skill from praxis cloud
> and walks the diagnosis with you)*

## Surface today (v0.4.x)

```
praxis init                       install skill into AI hosts +
                                   report state JSON
praxis login                      browser-callback authentication
                                   --profile X | --url Y | --token Z
praxis whoami                     live identity check via /auth/me
praxis status                     local snapshot (no network call)
praxis logout                     remove credentials
                                   --profile X | --all
praxis use <profile>              kubectl-style: set active profile
praxis install-skill              write the praxis skill into all
                                   detected AI hosts (Claude Code,
                                   Codex, Gemini CLI; user-scope only)
praxis uninstall-skill            remove from every host
praxis list-skills                what's installed and where
praxis refresh-skills             rewrite SKILL.md files (auto-called
                                   by `praxis update`)
praxis version | update | completion | logout | --help
```

## Multiple deployments (internal-support engineers)

```bash
praxis login                                            # → "default"
praxis login --profile acme --url https://acme.console.facets.cloud
praxis login --profile vymo --url https://vymo.console.facets.cloud

praxis use acme                # all subsequent commands use acme
praxis whoami                  # → reports acme's identity
praxis status                  # → reports acme's URL + auth state

# One-shot overrides (no `use` needed):
praxis whoami --profile vymo
PRAXIS_PROFILE=vymo praxis status
```

Single-profile users never see this — `praxis login` writes "default"
and everything resolves there silently.

### Files

```
~/.praxis/credentials   INI format, matches ~/.facets/credentials
                         [default]  url, username, token
                         [acme]     url, username, token
                         …
                         (chmod 0600)

~/.praxis/config        Active-profile pointer, set by `praxis use`.
                         Doesn't exist until first `use` call.
                         (chmod 0600)
```

## Why a CLI

CLIs run anywhere: any AI host, CI, cron, shell pipelines. MCP support
varies by tool; `bash -c "praxis …"` doesn't. The CLI also makes auth
and audit per-invocation, so every call is attributable.

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

Releases are cut by tagging `v*.*.*` and pushing — GitHub Actions runs
goreleaser, publishes the GitHub Release, and updates the Brew tap
formula automatically.

## License

MIT. See [LICENSE](LICENSE).
