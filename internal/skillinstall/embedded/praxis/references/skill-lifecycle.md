# Skill lifecycle and provenance

This `praxis` package is authored in praxis-cli at
`internal/skillinstall/embedded/praxis/` and embedded as a complete tree. References,
helpers and assets must travel with SKILL.md; copying the root alone loses the
capability. It is independent of server catalog credentials, so setup guidance
remains available offline. Raptor's canonical skill is owned/distributed by Raptor.

Explicit `praxis setup`, login and skill refresh install a missing Raptor skill
alongside Praxis in the selected host scope. They install a missing Raptor CLI
to `~/.local/bin/raptor` from its public release with SHA-256 verification, then
use that binary's `install-skills` in a disposable home. The complete exported
package is validated and installed transactionally. No separate skill-text copy
is maintained in Praxis. Check the PATH warning if the installed binary is not
yet discoverable by your shell. Silent first-use bootstrap stays Praxis-only
and offline; it does not run Raptor or download binaries.

Existing valid Raptor packages, including local-source symlinks, are preserved.
If a shared Codex/Gemini install would duplicate a peer's native Raptor package,
the missing host receives a native package in the same scope instead. Check the
returned installation paths; do not assume every host uses `.agents/skills`.
Invalid/shadowing copies need explicit repair. A binary containing only legacy
skills cannot supply the consolidated package: report incomplete setup and use
a Raptor release that contains it. Do not copy legacy global skills as a fallback.

CLI refresh also fetches org/personal skills and agent files from the selected
deployment. Those remain separate and are not made obsolete by a `praxis-` prefix.
Hosted Agent Factory pods still receive their server skill sources and execution
context. Do not replace pod skills with this local-CLI driver: pod access differs.

## Server export vs local migration

```text
Updated Agent Factory -> no legacy Praxis/Raptor GLOBALs to any CLI
                      -> web/pod skill sources retained
                      -> org/personal non-UI skills still export
Older server          -> may still return legacy GLOBALs; client filters them
```

Server CLI unsharing is unconditional, not dependent on the client's
`consolidated=praxis-v1,raptor-v1` tokens. Older CLIs must upgrade to get bundled
Praxis; install the independent Raptor skill for Facets work. A missing Raptor
package no longer makes the updated server send fallback definitions. A Raptor
executable alone is not proof its skill package exists.

Local replacement verification, capability tokens and scope-aware filtering
remain useful with older servers. They do not promise legacy server delivery or
authorize deleting every prefixed folder. [Legacy map](legacy-skill-map.md) resolves old task
packet names without adding another discovered wrapper skill per old name.

## Check and refresh

Inspect `praxis status --json` and `praxis list-skills --json` (use help for full
paths/scope). Distinguish repo source, embedded binary content, server catalog
version, on-disk package and the host's already-loaded skill context. Editing a
source checkout does not update an installed binary; a symlink may reflect source
edits immediately but is not a released distribution. Reload/restart host skill
discovery when necessary; don't claim this running conversation reloaded itself.

`praxis refresh-skills` updates the active scope's files; login/profile switching
can also do setup. Don't run any of these merely to inspect a skill. Before an
explicit repair/upgrade, establish the intended root, selected org and existing
user modifications. Do not refresh another org's skills into a concurrent session.

`praxis update` (alias `upgrade`) also invokes `raptor upgrade`, including when
Praxis is already current or is left to Homebrew. `--yes`/JSON passes `--yes`
to Raptor. JSON keeps each tool's result separate; Raptor failure does not undo
a completed Praxis update. Login installs missing binaries but does not upgrade
existing ones. After Praxis self-update, run setup using the new binary to get
its new embedded skill; the old running process cannot provide those bytes.
Installing a valid missing Raptor package is distinct from force-refreshing an
existing independent/local-source package. Do not infer the latter from login.

Installer migration must preserve uncertain/modified content and symlink targets,
stage replacements before retiring exact managed predecessors, and keep backups
outside host discovery directories. An incomplete fetch/install must not be
reported as a successful full refresh. Do not manually remove `praxis-*` globs,
edit other project roots, or change unrelated hooks/custom agents to test the
consolidation. Check returned warnings/paths for actual migration outcomes.

A successful complete catalog fetch also reconciles server-managed entries
that disappeared (for example after an org switch or revoked access). Current
org/personal namesakes remain; superseded scoped policy is archived outside
discovery so old-customer guidance does not stay active. This is distinct from
unsharing replaced globals. Inspect `skill_recovery_directory` and each backup's
`provenance.json` for its original path. Untracked or unproven ownership is not
permission for blanket cleanup; resolve any retained legacy context separately.

## Recovery and downgrade

Reconcile the installed tree/receipt and available backups before retrying a
failed refresh. Restore only the affected managed package, preserving later
user changes. Restoring a binary alone does not restore the old skill tree:
coordinate package and binary rollback. An older CLI can reinstall its legacy
embedded content on login/update, so verify canonical names and host discovery
after a downgrade. Do not delete Agent Factory seed files to unshare CLI content;
seeding can delete their GLOBAL records and break hosted users.
