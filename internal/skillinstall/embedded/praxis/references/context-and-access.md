# Context and access

Read for first login, profile ambiguity, authentication or project-local scope.
Start with `command -v praxis`, `praxis status --json`, and, for Facets work,
`raptor whoami`. Presence of stored credentials is not proof a live call succeeds.
Use the known console URL; never infer a customer's URL from its display name.

## Identity and scope

Profile precedence (raptor resolves the same order; its flag is `--profile`
only, because raptor's `-p` means **project**):

```text
--profile / -p
  > complete CONTROL_PLANE_URL + FACETS_USERNAME + FACETS_TOKEN
  > PRAXIS_PROFILE > FACETS_PROFILE
  > folder pin (nearest <dir>/.facets/profile above cwd, never $HOME's)
  > [default] > sole section
```

A selection writes nothing. Only `praxis profiles set-default NAME` and a bare
`praxis login` change `[default]`, the machine default; a login with
`--profile` saves credentials only. Never run either to switch profiles during
a task: other sessions on the machine use `[default]` too.

Facets control-plane PATs live in `~/.facets/credentials`, shared with Raptor.
A pin holds only a profile name. An older local mode left a token copy in
`<dir>/.facets/credentials`; both CLIs still read the first such file walking up
from cwd instead of the home file. Praxis API keys and loopback development
credentials use the home `.praxis/credentials`. If both stores contain the same
profile name, the Facets store's section wins. Use status to verify identity;
`praxis status --json` reports `profile_source` and `pin_path`.

On a machine with more than one profile, pass `--profile NAME` on every praxis
and raptor command, or work in a folder pinned to the profile
(`praxis profiles pin NAME`). An exported variable does not survive between
independent shell calls of an agent host. A command that selected nothing
prints one stderr line that names the profile it used.

Command identity and installed org-skill context are separate. The user-level
org skills belong to `[default]`; a pinned folder can hold its own
(`praxis login --profile NAME --local`, or `praxis refresh-skills` in the pinned
folder). Selecting another profile for one command does not install that org's
skills, and praxis prints a stderr line when the two orgs differ. Check
ownership before applying customer-specific guidance from a different installed
catalog.

## Setup when requested or needed

`praxis login --url https://CONSOLE` authenticates and performs setup; it can
reuse an existing Raptor PAT. Otherwise it opens the control plane's token page
in the browser and picks up the PAT the user creates there. Let the user finish
that step; never ask for a token in chat. Use installed help for CI credential
input; do not paste tokens into command arguments or transcripts.
For a new customer without a console, the access entrypoint is
[Facets signup](https://www.facets.cloud/signup).

Login, `profiles set-default` and `refresh-skills` also install a missing Raptor CLI (to
`~/.local/bin`) and run `raptor install skill`, so Raptor installs and maintains
its own skill. Check `raptor_binary`, `raptor_skills`, `raptor_warning` and
`snapshot_warning` in JSON: successful authentication does not prove both
tools/skills are ready.
See [skill lifecycle](skill-lifecycle.md) for installation, PATH and upgrade behavior.

After setup, inspect status and the live MCP manifest. Missing namespace, rejected
credentials, no integration and denied resource access are different problems.
Do not respond to an authorization failure by extracting credentials or switching
to another identity. Offer the exact missing connection or permission and resume
when available. Login/profile switching/skill refresh have local effects and are
not automatic recovery for every failed request.
