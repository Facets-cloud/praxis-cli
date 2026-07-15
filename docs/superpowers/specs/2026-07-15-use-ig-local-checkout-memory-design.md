# use-ig local-checkout memory + cwd hooks — Design

**Goal:** In the praxis-MCP read posture (reads run server-side via `praxis mcp
ig`, no local `ig` binary), (1) wire cwd hooks that nudge toward the `use-ig`
skill when the agent is sitting in a repo that belongs to an ig catalog, and
(2) help the agent turn a catalog node's repo-relative `file:line` into a real
local file.

## Problem

The MCP read tools return, for a node, a source path **relative to that member's
build root** (e.g. `UiDeploymentController.java:L449` under member
`control-plane`, whose build root may be a monorepo subdir). The server doesn't
know where — or whether — that repo is checked out on this machine, and an agent
grepping across repos won't reach for ig unless something reminds it. Two gaps:
a **discovery** gap (nothing tells the agent "this repo is an ig catalog member,
use the graph") and a **resolution** gap (mapping a member's node path to a
local file).

A first cut kept a local `ig-checkouts.json` and gated the hook on it. That is
fragile: an LLM can't be relied on to maintain JSON, so an absent/stale file
would silence the nudge forever. The hook must instead be **generic and
server-authoritative**.

## Non-goals

- No server changes in this MVP. The hook reuses the existing `praxis ig claims`
  gateway call.
- No `praxis ig workspace` verbs (deferred). The optional checkout scratchpad is
  agent-written; nothing in Go reads it.

## Architecture

A server-authoritative cwd hook, plus an optional agent-owned resolution note —
the hook scoped to the claude-code host:

```
                        catalog server ("praxis ig claims")
                                    ▲ authoritative membership
                    git origin ─────┘ (repo → catalogs)
                        │
   praxis ig hook <event>                 use-ig SKILL.md
   (wired at `praxis login`; nudges       (resolve a node → local file;
    toward use-ig when cwd's repo is       OPTIONAL ig-checkouts.json
    a catalog member)                      scratchpad — nothing depends on it)
```

### 1. The cwd hook — generic and server-authoritative

`praxis ig hook <session-start|cwd-changed>`, a hidden subcommand under
`praxis ig`, wired into Claude Code's settings.json. Reads `{cwd, session_id}`
on stdin. If cwd is a git repo, it canonicalizes the origin remote and asks the
**catalog server** — `igcatalog.Claims`, the same call as `praxis ig claims` —
which catalogs claim it. A member emits `hookSpecificOutput.additionalContext`
naming the claiming catalog(s) and pointing at the `use-ig` skill; silent +
exit 0 otherwise.

**Why server-side, not a local file.** An LLM can't be relied on to keep a local
JSON correct, so gating the nudge on agent-written state would let a
stale/absent file silence it forever. The server is the single source of truth
for membership; the hook reads no agent-maintained file.

**Cost control.** Bounded to one *successful* claims call per repo per session
via a `$TMPDIR` marker set only on success (an offline failure leaves it unset,
so the next cwd change retries). The call has a ~2.5s timeout and fails silent
(no auth / offline / slow → no nudge, never hangs a session start).

Canonicalization (scheme-less, `.git`-stripped, scp `git@host:path` →
`host/path`, lowercased) makes https/ssh origins hash to one identity for the
per-session marker.

### 2. The memory file — optional, skill-only

`~/.praxis/ig-checkouts.json` is the agent's OWN resolution scratchpad, to skip
re-finding a checkout across sessions. **No Go code reads or writes it and the
hook ignores it** — it is self-correcting (wrong → the agent re-discovers).
Keyed `"<catalog>/<member>" → {repo, path, dir}`, because the repo is not a
stable member identity: a member's name can differ between catalogs, and a
monorepo hosts several members from different subdirs of one repo.

```json
{
  "capillary-cloud/control-plane": { "repo": "…/control-plane.git", "path": "/src/control-plane", "dir": "." },
  "saas-cp/cp-svc":                { "repo": "…/control-plane.git", "path": "/src/control-plane", "dir": "." },
  "capillary-cloud/billing":       { "repo": "…/monorepo.git",      "path": "/src/monorepo",      "dir": "services/billing" }
}
```

**Resolution:** a node's absolute file is `path / dir / <node-relative-path>` —
graphify builds a member from its subdir, so node paths are relative to `dir`
(`.` for a whole-repo member), not the git root.

### 3. use-ig SKILL.md — "Resolving a node to a local file"

A short, non-prescriptive section: the node path is relative to the member's
build root (a subdir for a monorepo member); find the checkout (cwd
`git rev-parse --show-toplevel`, else scan `~/src`, `~/work`,
`~/praxis-envs/<profile>/…`), prepend the member subdir, and mind commit drift
(re-anchor by the symbol name ig prints, not a stale `L<n>`). The
`ig-checkouts.json` scratchpad is mentioned as an optional convenience only.

Guarded by `embedded_test.go`: the file must still contain `praxis mcp ig`, must
NOT contain a backticked `` `ig query` ``, and must mention `ig-checkouts.json`.

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

- No auth / gateway unreachable / slow claims → hook stays silent (exit 0),
  never marks the repo processed, so a later cwd change retries.
- cwd not a git repo / no origin → canonical URL is `""` → no server call, silent.
- Malformed settings.json at wire time → refuse to overwrite, warn, keep going.

## Testing (TDD)

- `CanonicalGitURL`: https/ssh/scp forms + `.git` suffix → same key.
- Hook core (`runIgHook`, injected claims fn): member nudges naming catalogs;
  non-member silent; no-origin silent AND skips the server; a claims error is
  silent and retryable (not marked processed); dedup → one server call + one
  nudge per repo per session.
- `installClaudeHooks`/`hookListUpsert`: fresh insert, idempotent re-run, stale
  path refresh, foreign-hook preservation, invalid-JSON refusal, uninstall.
- `embedded_test.go`: skill mentions `ig-checkouts.json`, still has `praxis mcp
  ig`, no backticked `ig query`.

## Deferred follow-ups (documented, not in this MVP)

1. **Claims caching across sessions** — a small TTL disk cache (incl. negative
   results) so repeated new sessions in the same repo skip the network entirely.
   MVP bounds calls to one per repo per session, which is already cheap.
2. **`praxis ig workspace set/list/resolve`** — promote the agent's checkout
   scratchpad to first-class CLI verbs (write/inspect/resolve a path).
3. **Server enrichment** — surface per-member `git` + `sha` + build subdir in
   the read tools so the agent resolves `dir` and staleness without guessing.
