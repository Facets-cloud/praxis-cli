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

## Surface today (v0.0.x — install + plumbing only)

```
praxis version              build version, commit, date, runtime
praxis update               self-update against GitHub Releases
praxis completion <shell>   bash | zsh | fish | powershell
praxis logout               delete ~/.praxis/credentials
praxis --help / -v
```

## Coming in subsequent releases

```
v0.1   skill list | show <name> | install [--host X] | uninstall | …
v0.2   login | whoami | mcp list | mcp <mcp> | mcp <mcp> <fn> [--arg val …]
       (server gateway must ship first)
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
