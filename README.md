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
   catalog (`praxis-<name>`), and does the same for custom agents. A login
   with `--profile` for a profile that is not the machine default skips this
   step and steps 5 and 6 (see "Profiles").
5. **Writes the MCP snapshot** `~/.praxis/mcp-tools.json`, so your AI can
   find gateway functions without a network call.
6. **Wires hooks:** in Claude Code, the ig session and directory hooks and a
   prompt hook that points the AI at the praxis skill; in Codex and Gemini
   CLI, the prompt hook. Codex runs a hook only after you trust it in its
   `/hooks` view. Antigravity has no hooks.

A control-plane PAT is saved to `~/.facets/credentials`, raptor's store,
under the praxis profile name. So `raptor` works with no second login, and a
`raptor login` is already a praxis login. A Praxis API key, or a PAT for a
loopback (local dev) control plane, goes to `~/.praxis/credentials`.

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
   folder (see "Local mode").

praxis refresh-skills [--project]
   Re-sync skills, agents and the MCP snapshot without re-authenticating.
   Installs at user level; inside a pinned folder it installs into that
   folder. --project pins the current folder to the active profile and
   installs there.

praxis profiles [--refresh]            list profiles (never prints tokens)
praxis profiles pin <name> | unpin     use a profile in this folder (both CLIs)
praxis profiles set-default <name>     change the machine default and re-sync
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

Selecting a profile writes nothing. Only `praxis profiles set-default` and a
bare `praxis login` change the machine default, `[default]`.

```text
1. -p/--profile flag                                    one command
2. CONTROL_PLANE_URL + FACETS_USERNAME + FACETS_TOKEN   raptor's env credential
3. $PRAXIS_PROFILE, then $FACETS_PROFILE                one shell or agent session
4. the folder pin, <folder>/.facets/profile             one folder and below it
5. [default]                                            the machine default
6. the only section, when there is exactly one
```

raptor resolves the same order (with `--profile` for the flag), so a
selection moves both CLIs. When `PRAXIS_PROFILE` and `FACETS_PROFILE` name
different profiles, both CLIs use `PRAXIS_PROFILE` and print a warning.

A misspelled selection fails with exit 3; praxis never falls back to another
profile. `logout` and `refresh-skills` refuse (exit 2, nothing changed) when
`-p` or `$PRAXIS_PROFILE` names a profile other than the one in effect.

## Profiles

```bash
praxis login --url https://root.console.facets.cloud                  # [default]
praxis login --profile acme --url https://acme.console.facets.cloud   # adds [acme]
praxis profiles                       # list; * marks the profile in effect here
praxis -p acme duty list              # one command against acme
export FACETS_PROFILE=acme            # this shell, both CLIs
praxis profiles pin acme              # this folder, both CLIs
praxis profiles set-default acme      # the whole machine
```

A login with `--profile` saves credentials only. It does not change
`[default]` or the installed org skills, so other sessions keep their profile.

**Org skills follow the machine default.** The user-level `praxis-*` skills
and agents belong to the profile `[default]` holds. `set-default` and a bare
`login` replace them; other selections do not. When a command uses a profile
from another org, praxis prints one line that says so. To get that org's
skills, pin a folder to it and run `praxis refresh-skills` there. The praxis
and raptor skills do not depend on the profile.

`set-default` checks the stored token first and changes nothing when it
cannot switch:

| Situation | Exit |
| --- | --- |
| token expired or revoked (run `praxis login --profile <name>`) | `3` |
| deployment unreachable | `5` |
| no such profile | `2` |

**Agents.** An agent shell loses `export` between commands. On a machine with
more than one profile, an agent passes `--profile` on each command or works in
a pinned folder. An agent command that selects nothing prints one line with
the profile it used.

**Shared with raptor.** Control-plane tokens live in `~/.facets/credentials`.
Both CLIs read the same store and the same selection order. `praxis logout`
and `profiles rm` remove the section from `~/.facets/credentials`, so raptor
is logged out of it too.

## Local mode

Local mode pins a profile to a folder, so different repos can work in
different orgs at the same time.

```bash
cd ~/work/acme-repo
praxis login --profile acme --local   # or: praxis profiles pin acme && praxis refresh-skills
```

It:

1. Saves the token in `~/.facets/credentials`, as any login does.
2. Writes `<repo>/.facets/profile` with the profile name only, and a
   `.gitignore` in `.facets`. Every praxis and raptor command in the repo then
   uses the profile. Only a control-plane PAT profile can be pinned.
3. Installs the profile's skills and agents into the repo
   (`<repo>/.claude/skills`, `<repo>/.agents/skills`, …), and keeps the skill
   receipt and MCP snapshot in `<repo>/.praxis/`. The raptor skill stays at
   user level. `praxis setup` refreshes the praxis skill in these repos after
   an upgrade.

Things to know:

- The repo must be under your home folder for the skill install; the home
  folder itself cannot be pinned.
- In a pinned folder, `praxis login --local` re-logs the pinned profile.
- An older raptor ignores pins. `pin` and `login --local` then warn with the
  raptor path and the upgrade command, and `praxis status` reports
  `pin_support: false` in its `raptor` block.
- `praxis profiles unpin` removes the pin. `praxis logout` in a pinned folder
  removes the pinned profile and the pin.
- An older praxis wrote `<repo>/.facets/credentials` (a token copy) for local
  mode. Both CLIs still read such a tree file, and a `--local` login there
  writes to it.
- `.facets/` and `.praxis/` each get a `.gitignore` that ignores the folder, so
  the pin, the receipt, backups and the MCP snapshot stay out of git.
  `.claude/` is not changed.

## Files

```text
~/.facets/credentials        control-plane PATs, shared with raptor (0600)
~/.praxis/credentials        Praxis API keys and loopback (local dev) PATs (0600)
~/.praxis/installed.json     receipt of every skill and agent file praxis wrote
~/.praxis/mcp-tools.json     MCP snapshot
~/.praxis/projects.json      project trees with a project install (refreshed after upgrades)
~/.praxis/backups/           copies of skills praxis replaced after you changed them

~/.claude/skills/praxis/                 praxis skill (Claude Code)
~/.claude/skills/praxis-<name>/          org skills (follow [default])
~/.agents/skills/…                       the same, for Codex and Gemini CLI
~/.gemini/config/skills/…                the same, for Antigravity
~/.claude/agents/, ~/.gemini/agents/     custom agents
~/.claude/settings.json, ~/.codex/hooks.json, ~/.gemini/settings.json
                                         hooks (praxis edits only its own entries)

~/.claude/skills/raptor/, ~/.agents/skills/raptor/, ~/.gemini/config/skills/raptor/
                                         raptor skill, owned by raptor
                                         (recorded in ~/.facets/raptor-skills.json)
```

In local mode, the pin, receipt, snapshot, skills and agents go into the
repo: `<repo>/.facets/profile`, `<repo>/.praxis/` and the repo's `.claude`,
`.agents` and `.gemini` folders. The token stays in `~/.facets/credentials`.

