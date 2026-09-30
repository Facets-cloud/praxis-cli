# Context and access

Read for first login, profile ambiguity, authentication or project-local scope.
Start with `command -v praxis`, `praxis status --json`, and, for Facets work,
`raptor whoami`. Presence of stored credentials is not proof a live call succeeds.
Use the known console URL; never infer a customer's URL from its display name.

## Identity and scope

Current Praxis profile precedence:

```text
--profile / -p
  > complete CONTROL_PLANE_URL + FACETS_USERNAME + FACETS_TOKEN
  > PRAXIS_PROFILE > FACETS_PROFILE > [default] > sole section
```

Facets control-plane PATs use `.facets/credentials`, shared with Raptor. Praxis
walks from cwd upward for the first project `.facets/credentials`, otherwise
uses the home file. Praxis API keys and loopback development credentials use
the home `.praxis/credentials`. There is no active-profile pointer file in the
current design. Older installed skills describing `.praxis` pointers are stale.
If both stores contain the same profile name, the selected Facets store's
section wins over the Praxis API-key section. Use status to verify identity;
the name alone does not distinguish the two credentials.

`praxis profiles use NAME` copies the selected identity over `[default]` and
refreshes the selected organization's installed catalog. For shared PATs this
also changes what an unscoped Raptor call uses. `--local` writes project-local
Facets credentials; a global switch can be shadowed by a project's file.
Read the switch result's effective scope, not just the requested profile name.

For concurrent work, prefer invocation-local selection, e.g.
`praxis --profile NAME mcp --json`, rather than changing shared defaults.
`PRAXIS_PROFILE` selects Praxis only; `FACETS_PROFILE` can select both CLIs,
subject to each CLI's precedence (the complete environment credential triplet
still matters). Keep the selected variables on each independent shell call.
Raptor's `-p` means **project**, not Praxis profile.

Command identity and installed org-skill context are separate. Selecting another
profile for one command does not install that org's skills. Check ownership
before applying customer-specific guidance from a different installed catalog.

## Setup when requested or needed

`praxis login --url https://CONSOLE` authenticates and performs setup; it can
reuse an existing Raptor PAT. The interactive secret prompt belongs in the user's
terminal, never chat. Let the user complete that prompt. Use installed help for
CI credential input; do not paste tokens into command arguments or transcripts.
For a new customer without a console, the access entrypoint is
[Facets signup](https://www.facets.cloud/signup).

Login also installs a missing Raptor CLI and its canonical skill. Check
`raptor_binary`, `raptor_skills`, `raptor_warning` and `skill_sync_complete` in
JSON: successful authentication does not prove both tools/skills are ready.
See [skill lifecycle](skill-lifecycle.md) for installation, PATH and upgrade behavior.

After setup, inspect status and the live MCP manifest. Missing namespace, rejected
credentials, no integration and denied resource access are different problems.
Do not respond to an authorization failure by extracting credentials or switching
to another identity. Offer the exact missing connection or permission and resume
when available. Login/profile switching/skill refresh have local effects and are
not automatic recovery for every failed request.
