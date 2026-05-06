# Praxis CLI

> Bring your organization's Praxis cloud to any local AI host —
> Claude Code, Cursor, Gemini CLI — without ever putting AWS, kubeconfig,
> or Terraform credentials on your laptop.

## What it does

```
  Your laptop                            Praxis cloud
  ───────────                            ────────────
  PRAXIS_TOKEN  ─────────────────────►   Validate per-call
                                         AssumeRole into your org's AWS
  Claude Code  ─ shells out to ─►        Execute the MCP function
  praxis CLI                             Audit
                ◄─── JSON response ───
```

`praxis` exposes Praxis MCP functions and skills as a single-binary CLI.
Your AI host calls `praxis mcp …` for any infra operation; the server
runs it under the org's managed credentials and returns the result.

## Install

```bash
brew install Facets-cloud/tap/praxis
# or
curl -fsSL https://install.askpraxis.ai | sh
```

Verify:

```bash
praxis version
praxis doctor
```

## Verify

```bash
praxis version
praxis completion zsh > "${fpath[1]}/_praxis"
```

The skill / MCP commands aren't shipped yet — see "Coming in subsequent
releases" below.

## Surface today (v0.3.x)

```
praxis install-skill        install the praxis skill into every detected
                             AI host (Claude Code, Codex, Gemini CLI).
                             User-scope only — Cursor has no user-level
                             skill dir.
praxis uninstall-skill      remove from every host where installed
praxis list-skills          show what's installed and where
praxis refresh-skills       rewrite installed SKILL.md files with current
                             content (called automatically by `praxis
                             update`)
praxis version              build version, commit, date, runtime
praxis update               self-update + auto-refresh installed skills
praxis completion <shell>   bash | zsh | fish | powershell
praxis logout               delete ~/.praxis/credentials
praxis --help / -v
```

## Try it

```bash
brew install Facets-cloud/tap/praxis
praxis install-skill          # writes the praxis skill to your AI hosts
praxis list-skills            # confirm where it landed
```

## Coming in subsequent releases

```
v0.2   login | whoami | mcp list | mcp <mcp> | mcp <mcp> <fn> [--arg val …]
       Server-driven skill catalog (skill list / show <name>); per-repo
       Cursor install. The placeholder "praxis" skill gets replaced with
       real catalogued skills (release-debugging, k8s-investigation,
       terraform-plan-explain, …).
```

## Why a CLI (and not an MCP server)?

CLIs run anywhere: any AI host, CI, cron, shell pipelines, your own scripts.
MCP support varies by tool; `bash -c "praxis …"` doesn't. The CLI also makes
auth and audit one-per-invocation, so every call is attributable.

## Develop

Requirements: Go 1.21+.

```bash
git clone https://github.com/Facets-cloud/praxis-cli.git
cd praxis-cli
make build           # ./praxis with version stamp
./praxis --help
make test
make lint            # gofmt + vet + test
```

Releases are cut by tagging `v*.*.*` and pushing — GitHub Actions runs
goreleaser, publishes the release, and updates the brew tap.

## License

MIT. See [LICENSE](LICENSE).
