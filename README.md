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

## Quick start

```bash
praxis login                                   # OAuth into your Praxis cloud
praxis skill install release-debugging         # install into all detected AI hosts
# Open Claude Code (or Cursor, or Gemini CLI):
#   "debug my failed prod release rel_8a2f1"
# Claude calls `praxis mcp …` under the hood. No laptop creds needed.
```

## Surface

```
AUTH      praxis login | logout | whoami
SKILLS    praxis skill list | show <name> | install <name> [--host X]
                  | uninstall <name> | list-installed | refresh
MCP       praxis mcp list                              list MCP servers
          praxis mcp <mcp>                             list functions in an MCP
          praxis mcp <mcp> <fn> [--key val …]          invoke a function
UTILITY   praxis doctor | update | completion | --help | version
```

Every command supports `--json` for machine-readable output. When stdout is
not a terminal, JSON is emitted by default — so AI hosts spawning praxis as
a subprocess always get parseable output.

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
