# praxis GTM bootstrap skill — Design

**Goal:** The moment praxis-cli is installed — **before any login** — the user's
AI host should know what praxis is, where to sign up, and how to log in. Today
zero skills install until `praxis login`, so a freshly-installed praxis is
invisible to the host (chicken-and-egg: the host can't learn to run `praxis
login` because the skill that would teach it only lands *after* login).

## Approach

Two parts: (1) an embedded, network-free **getting-started meta-skill**, and
(2) a **no-auth install path** that lands it at binary-install time.

```
 brew install praxis ─┬─► cask post-install hook ─► praxis setup ─► skill in ~/.claude
 praxis update        │
 raw download         └─► first `praxis` run (Execute-time) ─► praxis setup (if absent)
                                                                     │
                          host now knows: what praxis is, signup, `praxis login`
                                                                     │
                          user runs `praxis login` ─► full driver + org skills + MCP tools
```

## Components

### 1. The skill: `praxis-getting-started`

A binary-embedded, single-file **meta-skill** (like `praxis` / `praxis-memory`),
installed WITHOUT auth. No network — all copy is static. Product name: **Praxis
by Facets**. Sections:

- **What Praxis by Facets is** — you describe intent to your AI host; Praxis
  operates the cloud/infra server-side (via `praxis mcp`), with no cloud
  credentials on the laptop. What it can do (each framed as an "ask your AI host
  to…" example):
  - **Migrate from cloud A to B** — replatform infra across clouds.
  - **Bring manual infra under IaC** — adopt existing/brownfield resources into
    Terraform (discover → import → manage).
  - **Operate your cloud — you code, Praxis operates** — deploys, scaling, and
    day-2 ops from your AI host.
  - **Understand code ↔ infra** — who calls whom, FE→BE handlers, blast radius,
    what infra backs a service (the ig graph / `use-ig`).
- **Sign up** — `https://www.facets.cloud/signup`. You'll get your company's
  Praxis console URL (e.g. `https://<your-account-id>.console.facets.cloud`).
- **Log in** — `praxis login --url https://<your-account-id>.console.facets.cloud`.
  Also: `--profile <org>` (multiple orgs), `--token` (CI/non-interactive).
- **After login** — your org's MCP tools (`praxis mcp --json`) and skills
  (blueprints/raptor, ig graph reads, memory) appear, plus the full `praxis`
  driver skill that supersedes this one.
- **Check** — `praxis --version`, `praxis profiles`, `praxis status`.

It is a **meta-skill**: preserved on profile switch and on logout (`IsMetaSkill`),
so the host can always find its way back to login. `praxis login` installs the
richer `praxis` driver skill alongside it.

Pre-login capabilities are described in **categories** only — the exact MCP tools
are org-scoped and knowable just after login, so the skill says "log in to see
your org's tools."

### 2. `BootstrapSkillNames()` + `praxis setup` (hidden)

- `skillinstall.BootstrapSkillNames()` returns the no-auth-installable subset:
  `["praxis-getting-started"]` (a strict subset of `MetaSkillNames()`).
- `praxis setup` — a **hidden** command (`init` was removed in the major-version
  cleanup and must not return — see root_test.go), **no credentials required**: detect
  AI hosts (reuse `harness.Detected()`), install every bootstrap skill into each,
  write the first-run marker. Idempotent; prints where it installed. This is the
  single install primitive both the cask hook and first-run call.

### 3. First-run auto-install (`Execute()`-time)

Covers installs the cask hook can't reach (`praxis update`, raw binary download):

- Gate on a marker `~/.praxis/.bootstrap-v1`. Present → a single `stat()`, done.
- Absent → run the same bootstrap install, then write the marker. **Never fatal**
  — a failure must not block the actual command.
- **Skip machine-invoked commands** so hot/side-effect-sensitive paths stay clean:
  `ig` (covers `ig hook` — first positional is `ig`), `mcp`, `completion`,
  `__complete`, `git-credential`, `setup`, `version`, `update`. (The cwd hook must
  never trigger a skill write mid-session.)
- The marker is versioned (`-v1`) so a future bootstrap-content bump can re-run
  once by bumping the suffix.

### 4. Homebrew cask post-install hook

Extend the existing `hooks.post.install` (which already strips quarantine) to
invoke `praxis setup` after the binary is linked, so `brew install praxis` lands
the skill with zero user steps. Non-fatal (`|| true` semantics) — a hook failure
must not fail the brew install.

## Lifecycle

- `praxis setup` / first-run / cask → installs `praxis-getting-started` only.
- `praxis login` → installs the full meta + org skills (superset), idempotent
  over the bootstrap skill.
- `praxis logout` → removes org + non-meta skills but KEEPS meta-skills, so
  `praxis-getting-started` and the `praxis` driver survive (host can re-login).

## Error handling

- No AI host detected → `praxis setup` says so and exits 0 (nothing to do).
- First-run install failure → warn to stderr at most once, never block the command.
- Cask hook failure → swallowed; brew install still succeeds.

## Testing (TDD)

- Embedded: `BootstrapSkillNames()` returns `praxis-getting-started`; it is a
  subset of `MetaSkillNames()`; the skill is `IsMetaSkill` (survives logout).
- Content guard: the skill body mentions the signup URL (`facets.cloud/signup`),
  `praxis login`, and does NOT assume a logged-in state.
- `praxis setup`: installs bootstrap skills into stubbed hosts with NO credentials
  present; idempotent; "no hosts" → exit 0 with a message.
- First-run: marker absent + human command → installs + writes marker; marker
  present → no install; a denylisted command (`ig`, `mcp`) → no install even
  with the marker absent; failure is non-fatal.

## Deferred / out of scope

- Dynamic, org-specific capability listing in the pre-login skill (needs a
  network call; pre-login stays static + generic).
- `brew uninstall` zap to remove the skill (leave the skill until praxis is
  re-installed or the user removes it).
- A hosted self-serve signup distinct from the login deployment URL.

## Sequencing

Bundled with the cwd-hook work on `feat/use-ig-local-checkout-memory` → one
`v1.4.3` release (per decision).
