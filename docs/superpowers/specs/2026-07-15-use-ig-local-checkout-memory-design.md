# use-ig local-checkout memory + cwd hooks — Design

**Goal:** In the praxis-MCP read posture (reads run server-side via `praxis mcp
ig`, no local `ig` binary), let an agent turn a catalog node's repo-relative
`file:line` into a real local file — and REMEMBER the member→local-checkout
mapping so it doesn't re-discover every session. Plus wire cwd hooks that nudge
toward the `use-ig` skill when the agent is sitting in a remembered checkout.

## Problem

The MCP read tools return, for a node, a source path that is **relative to that
member's repo root** (e.g. `UiDeploymentController.java:L449` under member
`control-plane`). The server does not know where — or whether — that repo is
checked out on this machine. Today the agent re-derives the checkout root by
hand every time (grep `git remote`, guess directories). The old host-`ig`
posture kept a local `workspace.yaml` (git-URL → checkout map); the MCP redesign
dropped it. This restores that memory as a host-local cache the agent owns,
without reintroducing a local `ig`.

## Non-goals

- No new network calls. Reads still go through `praxis mcp ig`; this is purely a
  host-local record + a nudge.
- No server changes in this MVP. (Surfacing per-member `git` + `sha` in the read
  tools' `ig_list_catalogs` output is a **deferred follow-up** — see below.)
- No `praxis ig workspace set/list/resolve` verbs (deferred). The agent writes
  the memory file directly, guided by the skill.

## Architecture

Two host-local pieces, both scoped to the claude-code host:

```
                     ~/.praxis/ig-checkouts.json   (the memory; agent owns it)
                       │  { "<git-remote>": {path, member, catalog} }
        writes ◄───────┘                    └──────► reads
          │                                            │
   use-ig SKILL.md                          praxis ig hook <event>
   (LLM: discover → remember → reuse)       (wired at `praxis login`,
                                             nudges when cwd matches a
                                             remembered checkout)
```

### 1. The memory file: `~/.praxis/ig-checkouts.json`

Global (machine-level), even under `--local` folder-per-login — credentials
already live at `~/.praxis/credentials`, so `~/.praxis` is the stable praxis
home. A JSON object keyed by the checkout's git remote URL:

```json
{
  "https://github.com/org/control-plane.git": {
    "path": "/Users/me/src/control-plane",
    "member": "control-plane",
    "catalogs": ["capillary-cloud", "saas-cp"]
  }
}
```

- `path` — absolute checkout root (`git -C <dir> rev-parse --show-toplevel`).
- `member` — the lens the path resolves for.
- `catalogs` — a **list**: one checkout can be a member of several catalogs
  (control-plane is in both capillary-cloud and saas-cp), so the agent unions
  each catalog it encounters rather than overwriting. Path resolution is
  catalog-independent; the list only enriches the nudge.
- The **key** is whatever `git remote get-url origin` prints; the hook
  canonicalizes both sides on read (scheme-less, `.git`-stripped, scp-form
  `git@host:path` → `host/path`, lowercased) so https/ssh forms still match.

The agent (via the skill) owns all writes. The hook only reads.

### 2. use-ig SKILL.md — "Resolving a catalog node to a local file"

New section teaching the agent, in order:

1. **Read memory first.** Load `~/.praxis/ig-checkouts.json`; if the member is
   present, use its `path` and skip discovery.
2. **Discover if absent.** Prefer cwd (`git -C . rev-parse --show-toplevel` when
   its `origin` matches the member); else scan likely roots (`~`, `~/src`,
   `~/work`, folder-per-login `~/praxis-envs/<profile>/`) for a checkout whose
   remote or dir name matches the member.
3. **Remember it.** Merge one entry into `~/.praxis/ig-checkouts.json`.
4. **Reuse + staleness.** Next session, read the file first. The catalog was
   built at some past commit; if the local checkout has advanced well past it,
   treat `L<n>` line numbers as approximate and re-anchor by the **symbol name**
   ig prints, not the raw line. (Precise sha-based staleness is a deferred
   follow-up; it needs the server to surface per-member `sha`.)

Guarded by `embedded_test.go`: the file must still contain `praxis mcp ig`, must
NOT contain a backticked `` `ig query` ``, and (new) must mention
`ig-checkouts.json`.

### 3. `praxis ig hook <session-start|cwd-changed>` — the nudge handler

A hidden subcommand under the existing `praxis ig` tree (mirrors `ig hook`).
Reads `{cwd, session_id}` JSON on stdin. Canonicalizes the cwd's `origin`
remote, looks it up (canonicalized) in `~/.praxis/ig-checkouts.json`. On a
match, emits `hookSpecificOutput.additionalContext` nudging toward the `use-ig`
skill (naming the member + catalog); deduped per session via a `$TMPDIR`
sentinel. Silent + exit 0 otherwise — a hook must never block a session.

**Gate difference from ig:** ig nudges when cwd is a member of a *locally-built
catalog* (reads `$IG_HOME/projects/*`). Praxis has no local catalog, so it
nudges only when cwd matches a *remembered* checkout — **quiet until the agent
has recorded that repo at least once.** That is the intended behavior: the first
resolution teaches the memory; thereafter the hook reminds.

### 4. Wiring at `praxis login`

Port ig's `wireClaudeHooks`/`installClaudeHooks` into the login skill-install
flow, claude-code host only (settings.json hooks are Claude-Code-specific):

- settings.json path = `filepath.Dir(host.SkillDir)/settings.json` — resolves to
  `~/.claude/settings.json` (user scope) or `<projectDir>/.claude/settings.json`
  (`--local`), matching the folder-per-login posture.
- Hook command = `<praxisPath> ig hook <event>` (`os.Executable()` +
  `EvalSymlinks`).
- `SessionStart` (matcher `startup|resume`) + `CwdChanged` (no matcher).
- `isPraxisHookCommand` guards against clobbering another tool's hook: the
  command must end in ` ig hook <event>` AND its argv[0] basename must be
  `praxis`.
- `hookListUpsert` keeps exactly one praxis entry per event (refreshes a stale
  binary path, never duplicates). Additive/idempotent; other hooks and top-level
  keys untouched; previous file kept as `settings.json.bak`.
- Never fatal — a failed wire warns and continues; skills still install.
- **Logout** unwires the same two entries (symmetric; leaves other hooks intact).

## Error handling

- Malformed `~/.praxis/ig-checkouts.json` → hook stays silent (exit 0), never
  errors a session. The skill tells the agent to overwrite a corrupt file.
- Malformed settings.json at wire time → refuse to overwrite, warn, keep going.
- cwd not a git repo / no origin → canonical URL is `""` → no match → silent.

## Testing (TDD)

- `canonicalGitURL`: https/ssh/scp forms + `.git` suffix → same key.
- Hook handler: match emits nudge JSON with member+catalog; no-match silent;
  dedup suppresses second call same session; malformed file silent.
- `installClaudeHooks`/`hookListUpsert`: fresh insert, idempotent re-run, stale
  path refresh, foreign-hook preservation, invalid-JSON refusal.
- `embedded_test.go`: skill mentions `ig-checkouts.json`, still has `praxis mcp
  ig`, no backticked `ig query`.

## Deferred follow-ups (documented, not in this MVP)

1. **Server enrichment** — surface per-member `git` + `sha` in the read tools'
   `ig_list_catalogs` so the agent can (a) discover the remote without a local
   checkout and (b) do precise sha-vs-HEAD staleness.
2. **`praxis ig workspace set/list/resolve`** — promote the agent-written memory
   to first-class CLI verbs (write/inspect/resolve a path from the shell).
3. **Project-scoped memory** — read a `<projectRoot>/.praxis/ig-checkouts.json`
   overlay on top of the global file.
