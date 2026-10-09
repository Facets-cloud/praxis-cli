# Praxis CLI

> Bring your Praxis cloud to any local AI host: Claude Code, Codex,
> Gemini CLI and Antigravity. Your AI operates the CLI; you install the
> binary and sign in once.

## What your AI can do

After `praxis login`, your AI host can:

- **Use the praxis and raptor skills.** The `praxis` skill ships inside
  this binary. Login also installs the Raptor CLI and its `raptor` skill.
  Together they cover Kubernetes and cloud investigation, New Relic, duties,
  custom agents, migrations, task DAGs, Facets blueprints, modules and
  releases.
- **Use your org's own skills.** Skills that your organization (or you)
  publish on Praxis install as `praxis-<name>` on every login.
- **Call the Praxis gateway.** `praxis mcp` lists and calls server-side
  functions under your org's credentials: read-only `kubectl`/`helm`
  (`k8s_cli`), read-only `aws`/`gcloud`/`az` (`cloud_cli`), New Relic,
  GitHub (`vcs_cli`), Slack, duties, task DAGs, artifacts, ig catalogs and
  more. No kubeconfig or cloud keys on your laptop. Run `praxis mcp --json`
  for the live list.
- **Drive Facets with raptor.** Raptor is a local CLI that uses the same
  control-plane token. The control plane enforces RBAC and audit.
- **Use org-curated agents.** Custom agents from your Praxis profile install
  as subagents for Claude Code and Gemini CLI. List them with `praxis agents`.
- **Read and write org memory** (`praxis memory`), triage duty runs and
  findings (`praxis duty`), sync ig catalogs (`praxis ig`), and push to
  GitHub, GitLab or Bitbucket with short-lived tokens
  (`praxis git-credential`).

## Install

**Linux, or macOS without Homebrew:**

```bash
curl -fsSL https://cross.facetsapp.cloud/cli/install.sh | sh -s -- praxis
```

The script downloads the release for your platform, checks its SHA-256 and
puts it in `~/.local/bin`, without sudo. If `~/.local/bin` is not on your
PATH, it adds one line to your shell profile; open a new terminal after it.
Set `FACETS_INSTALL_DIR` to use another folder, or `FACETS_NO_MODIFY_PATH=1`
to leave your profile alone.

**Windows** (PowerShell, no administrator rights):

```powershell
irm https://cross.facetsapp.cloud/cli/install.ps1 | iex
```

The script downloads `praxis.exe` (x64 or ARM64), checks its SHA-256 and
puts it in `%USERPROFILE%\.local\bin`. It adds that folder to your user PATH
and to the current window; other terminals need a restart. Claude Code on
Windows needs [Git for Windows](https://git-scm.com/download/win).

**macOS with Homebrew:**

```bash
brew install --cask Facets-cloud/tap/praxis
```

Keep one install method. The script tells you when a Homebrew praxis is also
on your PATH.

**Updates.** praxis updates itself: when a newer release exists, it updates
in the background after a command, for the next command. It does not do this
in CI, in a container, as root, or when you cannot write to its folder; there
it tells you to run `praxis update`. Set `PRAXIS_NO_AUTO_UPGRADE=1` to turn
it off. `praxis update` updates praxis (with `brew` for a Homebrew install),
then raptor.

Latest release: <https://github.com/Facets-cloud/praxis-cli/releases/latest>.

## Set up

```bash
praxis login
```

Login is idempotent; re-run it at any time. It:

1. **Installs raptor** to `~/.local/bin` when it is missing, and asks raptor
   to install its `raptor` skill into every detected AI host.
2. **Installs the praxis skill** into every detected AI host.
3. **Authenticates** with a control-plane personal access token (PAT):
   - When `raptor` is already logged in, login reuses that token and control
     plane. Nothing to click.
   - Otherwise it opens the control plane's personal-access-token page, the
     same page `raptor login` opens. Create a token there and the CLI picks
     it up. A new profile needs the control plane URL: pass `--url`, or
     login asks for it on a terminal.
   - `--token` saves an existing Praxis API key instead. Login never creates
     a new Praxis API key.
4. **Replaces the org skills** of the previous profile with this profile's
   catalog (`praxis-<name>`), and does the same for custom agents.
5. **Writes the MCP snapshot** `~/.praxis/mcp-tools.json`, so your AI can
   find gateway functions without a network call.
6. **Wires hooks:** in Claude Code, the ig session and directory hooks and a
   prompt hook that points the AI at the praxis skill; in Codex and Gemini
   CLI, the prompt hook. Codex runs a hook only after you trust it in its
   `/hooks` view. Antigravity has no hooks.

A control-plane PAT is saved to `~/.facets/credentials`, raptor's store,
under the praxis profile name. So `raptor` works with no second login, and a
`raptor login` is already a praxis login. A Praxis API key goes to
`~/.praxis/credentials`.

```bash
praxis login --url https://<account-id>.console.facets.cloud   # first time
```

Ask your Praxis administrator if you do not know the URL. Later logins reuse
the saved URL. `praxis refresh-skills` re-syncs skills and the MCP snapshot
without re-authenticating; use it after your org publishes new skills.

Then open your AI host and ask, for example, "show me what's deployed in
prod" or "debug my failed release".

## Commands

AI-facing commands accept `--json`, which is the default when stdout is not a
terminal.

```text
praxis login [--url U] [--token T] [--local] [--dry-run] [--force]
   Set up or refresh this profile (see "Set up"). --dry-run reports what
   login would do and exits (0 = report complete, 5 = server unreachable).
   --force skips the stored token. --local pins the profile to the current
   directory tree (see "Local mode").

praxis refresh-skills [--project]
   Re-sync skills, agents and the MCP snapshot without re-authenticating.
   Installs at user level; inside a local-mode tree it scopes to that tree.
   --project pins the current directory to the active profile and installs
   there.

praxis profiles [--refresh]            list profiles (never prints tokens)
praxis profiles use <name> [--local]   switch the active profile and re-sync
praxis profiles rename OLD NEW         rename a profile, credentials only
praxis profiles rm NAME                delete a non-active profile's credentials
praxis logout [--all]                  remove the active profile's credentials
                                       (--all: every profile), org skills,
                                       agents, hooks and MCP snapshot; the
                                       praxis skill stays

praxis status [--refresh] [--full]     profile, auth, installed skills, and the
                                       raptor profile a bare raptor would use
praxis list-skills                     installed skills and where they live
praxis agents                          installed custom agents

praxis mcp                             list gateway functions
praxis mcp <mcp> <fn> [--arg k=v ...] [--body '<json>' | -]
                                       call one function
praxis memory recall|list|add ...      org memory (JSON only)
praxis duty list|runs|run|report|findings ...
                                       duty runs, findings and reports (read-only)
praxis ig list|sync|status ...         ig catalogs
praxis git-credential                  git credential helper (see its --help)
praxis report -m "<what happened>"     send a friction report to the Praxis team

praxis update [-y]                     update praxis, then raptor
praxis version | completion <shell> | help
```

### Profile selection

`-p, --profile <name>` uses a profile for one command and writes nothing:
`praxis -p acme duty list`. `PRAXIS_PROFILE=<name>` does the same for every
command in one shell or agent session. Both are safe with concurrent sessions.

The active profile resolves in this order (first match wins):

```text
1. -p/--profile flag
2. CONTROL_PLANE_URL + FACETS_USERNAME + FACETS_TOKEN   raptor's env credential
3. $PRAXIS_PROFILE
4. $FACETS_PROFILE                                      raptor's selector; moves both CLIs
5. [default] of the store in effect                     the tree's file inside a local tree
6. the only section, when there is exactly one
```

A misspelled `-p` or `$PRAXIS_PROFILE` fails with exit 3; praxis never falls
back to another profile. `logout` and `refresh-skills` refuse (exit 2,
nothing changed) when `-p` or `$PRAXIS_PROFILE` names a profile other than
the active one.

## Profiles

> **Whatever changes the active profile also re-installs the skills.**

The active profile is the `[default]` section of the credentials store, the
same rule raptor uses. Only `praxis login` and `praxis profiles use` change it.
Both copy the profile's section over `[default]`, remove the previous
profile's `praxis-*` skills and agents, and install the new profile's. The
praxis and raptor skills do not depend on the profile and stay. Hand-editing
`[default]` is not a switch: it leaves the old profile's skills installed.

```bash
praxis login --profile acme --url https://acme.console.facets.cloud   # add; becomes active
praxis profiles                  # list
praxis profiles use acme         # switch back, no browser
praxis -p bigcorp duty list      # one command against another profile
```

`profiles use` checks the stored token first and changes nothing when it
cannot switch:

| Situation | Exit |
| --- | --- |
| token expired or revoked (run `praxis login --profile <name>`) | `3` |
| deployment unreachable | `5` |
| no such profile | `2` |

**Several sessions at once.** `profiles use` is machine-wide: it rewrites
`[default]` and the installed skills for every shell and agent session.
To run sessions against different profiles, set `PRAXIS_PROFILE` in each
instead. A session scoped this way still reads the skill files of the
globally active profile; for another org's skills in one repo, use local
mode.

**Shared with raptor.** Control-plane tokens live in `~/.facets/credentials`,
and `praxis profiles use` moves raptor too, because both read `[default]`.
The CLIs differ only for praxis-only selectors (`-p`, `$PRAXIS_PROFILE`):
`praxis status --json` then reports, in its `raptor` block, whether the AI
host must run raptor as `FACETS_PROFILE=<profile> raptor …`. `praxis logout`
and `profiles rm` remove the section from `~/.facets/credentials`, so raptor
is logged out of it too.

## Local mode

Local mode pins a profile to a directory tree, so different repos can work
in different orgs at the same time.

```bash
cd ~/work/acme-repo
praxis profiles use acme --local      # or: praxis login --profile acme --local
```

It:

1. Writes `<repo>/.facets/credentials` with the profile's section and a
   `[default]` copy, plus a `.gitignore`. This is the file
   `raptor login --local` writes, and inside the tree both CLIs read it
   instead of the home store. Only a control-plane PAT profile can be pinned.
2. Installs the profile's skills and agents into the repo
   (`<repo>/.claude/skills`, `<repo>/.agents/skills`, …). The raptor skill
   stays at user level.
3. Keeps the skill receipt and MCP snapshot in `<repo>/.praxis/`.

Things to know:

- The repo must be under your home folder; `--local` refuses other folders
  (exit 2). Symlinks are resolved first.
- `login` and `profiles use` without `--local` change the home store, also
  when run inside a local tree. Their output then shows
  `shadowed_by_project_root`, because the tree's file still wins there.
- To detach a repo, delete its `.facets` and `.praxis` folders.
- Add `/.praxis/` to the repo's `.gitignore`. A `.praxis/` folder without
  `.facets/credentials` beside it does nothing.

## Files

```text
~/.facets/credentials        control-plane PATs, shared with raptor (0600)
~/.praxis/credentials        Praxis API keys only (0600)
~/.praxis/installed.json     receipt of every skill and agent file praxis wrote
~/.praxis/mcp-tools.json     MCP snapshot
~/.praxis/backups/           copies of skills praxis replaced after you changed them

~/.claude/skills/praxis/                 praxis skill (Claude Code)
~/.claude/skills/praxis-<name>/          org skills (change with the profile)
~/.agents/skills/…                       the same, for Codex and Gemini CLI
~/.gemini/config/skills/…                the same, for Antigravity
~/.claude/agents/, ~/.gemini/agents/     custom agents
~/.claude/settings.json, ~/.codex/hooks.json, ~/.gemini/settings.json
                                         hooks (praxis edits only its own entries)

~/.claude/skills/raptor/, ~/.agents/skills/raptor/, ~/.gemini/config/skills/raptor/
                                         raptor skill, owned by raptor
                                         (recorded in ~/.facets/raptor-skills.json)
```

In local mode, credentials, receipt, snapshot, skills and agents move into the
repo: `<repo>/.facets/credentials`, `<repo>/.praxis/` and the repo's
`.claude`, `.agents` and `.gemini` folders.

## Security: keep credentials out of transcripts

This repo's [`.claude/settings.json`](.claude/settings.json) stops Claude
Code from reading credential files: `~/.facets/`, `~/.praxis/credentials`,
`~/.aws/`, `~/.config/gcloud/`, `~/.azure/` and key files (`*.pem`, `*.key`,
`id_rsa*`, `id_ed25519*`, `*.token`). The rest of `~/.praxis/` stays readable,
because the praxis skill reads `~/.praxis/mcp-tools.json`.

These rules apply only inside this repo. To apply them to every Claude Code
session, merge the `permissions.deny` entries into your
`~/.claude/settings.json` (or copy the file if you have none). A `Read` or
`cat` of a credentials file puts the token into the session transcript under
`~/.claude/projects/`, which can be synced or shared.

If a token gets into a transcript: revoke it and create a new one on the
control plane's personal-access-token page, then delete that session file
or replace the token in it.

## Develop

Requirements: Go 1.24+.

```bash
git clone git@github.com:Facets-cloud/praxis-cli.git
cd praxis-cli
make build    # ./praxis with the version stamped in
make test     # go test -race ./...
make lint     # golangci-lint (pinned version)
make check    # fmt, vet, lint, test
```

To release, push a `v*.*.*` tag. GitHub Actions runs goreleaser, publishes the
GitHub release and updates the Homebrew cask. The install scripts and the
automatic update pick the release up from cross.facetsapp.cloud within about
ten minutes.

## License

MIT. See [LICENSE](LICENSE).
