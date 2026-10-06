# Skill lifecycle

Read when a skill seems missing, stale or duplicated, or before a repair.

## Who owns which skill

| Skill | Owner | Source |
|---|---|---|
| `praxis` (this package) | praxis CLI | embedded in the `praxis` binary |
| `raptor` | raptor CLI | embedded in the `raptor` binary |
| `praxis-<name>` | the organization | fetched from the selected deployment |

Each owner installs and updates only its own skills. Hosted Agent Factory pods
get their skills from the server; this package is for local CLI hosts.

## Where skills go

| Host | Skill folder |
|---|---|
| Claude Code | `~/.claude/skills` |
| Codex, Gemini CLI | `~/.agents/skills` (shared) |
| Antigravity | `~/.gemini/config/skills` |

A project-scoped login (`praxis login --local`, `praxis refresh-skills --project`)
puts the praxis and organization skills under the repo instead. The raptor skill
always goes to the user level.

## When the praxis skill is installed

- The first run of any praxis command, once and offline. It is skipped for
  `mcp`, `ig`, `git-credential`, `setup`, `version`, `update` and `completion`.
- `praxis setup`. Homebrew runs it on install and on every upgrade.
- `praxis login`, `praxis refresh-skills` and `praxis profiles use`.
- Any other praxis command, when the installed praxis skill was written by a
  different praxis binary (after a brew upgrade, `praxis update` or a manual
  install). The command first rewrites the skill, silently. A skill you edited
  does not trigger this, because the check uses the digest the receipt recorded
  at install. It is skipped for development builds and for `login`, `logout`,
  `refresh-skills`, `profiles`, `setup`, `update`, `hook`, `git-credential`,
  `version` and `completion`.

Each install also removes the embedded skills this package replaced:
`praxis-getting-started`, `praxis-memory`, `praxis-onboarding` and `use-ig`.

## When organization skills change

Login, `refresh-skills` and `profiles use` fetch the selected organization's
skills and agent files. A failed fetch leaves the installed ones in place. A
successful fetch replaces the previous profile's `praxis-<name>` skills.
`praxis logout` removes them, the agent files and the hooks. It keeps `praxis`.

## The raptor skill

Login, `refresh-skills`, `profiles use` and `setup` run
`raptor install skill --agent <claude|codex|gemini>` for each detected host.
They also install a missing raptor to `~/.local/bin`. Raptor writes
`~/.<agent>/skills/raptor` and records the path in
`~/.facets/last-skills-upgrade`. After a raptor upgrade, the next ordinary raptor
command rewrites every recorded path. `praxis setup` and `praxis update` upgrade
raptor and refresh its skill at once.

- A Codex or Gemini host that already reads `~/.agents/skills/raptor` is skipped,
  so it never sees two raptor skills.
- Antigravity has no raptor skill.
- Raptor older than 0.1.107 cannot install its skill; run `praxis update`.

## How installs are protected

Writes are staged, locked and committed through the receipt
(`~/.praxis/installed.json`, or `<repo>/.praxis/installed.json`). The receipt
records each skill's source and a digest of its files. Before praxis replaces or
removes a skill folder that does not match its digest, it copies the folder to
`~/.praxis/backups/skill-*/` (or `<repo>/.praxis/backups/`), with a
`provenance.json` that names the original path. A skill folder that is a symlink
is never replaced or written through; the install fails instead.

## Check and repair

- `praxis status --json` shows `skills_installed` and `agents_installed`.
- `praxis list-skills --json` lists the receipt.
- `raptor --version` and `~/.facets/last-skills-upgrade` show the raptor side.

A running host can keep the old skill text until it reloads its skills. Run
`praxis refresh-skills` only to repair, not to inspect. Restore a changed skill
from its backup folder, using `provenance.json` to find where it came from.
