# Changelog

All notable changes to praxis CLI are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

(Empty — see 0.7.0 below.)

## [0.7.0] — 2026-05-08

Major surface simplification. The CLI now ships with **8 visible
commands** (down from 16) and a single design invariant:

> **Login is the only mutator of installed-skill state. The CLI's
> on-disk state always matches the active profile's view of the world.**

### Added
- `praxis login` is now the one-shot setup command. In addition to
  authenticating, it also: (a) installs the praxis meta-skill into
  every detected AI host idempotently, (b) wipes any praxis-* org
  skills from the previous profile, (c) fetches and installs this
  profile's skill catalog, and (d) refreshes
  `~/.praxis/mcp-tools.json`. Re-run login any time to refresh.
- `praxis status --refresh` adds a live `/ai-api/auth/me` check,
  absorbing the value the deprecated `whoami` provided. The local
  snapshot remains the default (no network calls).
- `--json` output on `logout`, `update`, and `version` (with stable
  schemas). `update --json` implies `--yes` since AI hosts can't
  answer the interactive confirmation prompt.
- `internal/skillinstall.UninstallByPrefix(prefix)` — primitive used
  by login (to wipe previous-profile org skills) and logout (to
  remove org skills alongside credentials). Always preserves the
  meta-skill ("praxis", no suffix).

### Changed (breaking — surface only; behaviors are aliased through v0.7)
- The user-facing command surface shrinks to:
  `login`, `logout`, `status`, `mcp`, `update`, `version`,
  `completion`, plus cobra's `help`. All of these except `login` and
  `completion` accept `--json`.
- `praxis logout` now also removes the active profile's org skills
  (praxis-* prefix) and deletes `~/.praxis/mcp-tools.json`. The
  praxis meta-skill stays. `--all` wipes every profile's credentials
  AND every org skill across every host.
- Profile switching is now `praxis login --profile X` — login wipes
  the previous profile's org skills and installs X's catalog. There
  is no concurrent multi-profile installed state on disk anymore.

### Deprecated (still functional in v0.7, removed in v0.8)
- `praxis init` → use `praxis login` (idempotent meta-skill install)
- `praxis install-skill` → use `praxis login`
- `praxis uninstall-skill` → use `praxis logout`
- `praxis refresh-skills` → use `praxis login` (it refreshes everything)
- `praxis whoami` → use `praxis status --refresh`
- `praxis use` → use `praxis login --profile X`
- `praxis list-skills` → use `praxis status`
- `PRAXIS_PROFILE` env var — login is now the only way to switch
  profiles. The env var still works in v0.7 (with a stderr warning)
  but is ignored from v0.8.

All deprecated commands continue to function in v0.7 with a stderr
warning pointing at the v0.7 replacement. They are hidden from
`praxis --help` so the visible surface is the v0.7 surface only.

### Meta-skill body rewrite
The praxis meta-skill (the SKILL.md installed into every AI host)
has been rewritten to teach AI hosts the v0.7 surface. The old body's
references to install-skill / refresh-skills / whoami / use / init
are gone; the body now teaches: "brew install + praxis login is the
whole setup; re-run login to refresh; logout to revoke."

### Tests
- New `internal/skillinstall.UninstallByPrefix` test pinning the
  meta-skill survival contract.
- New `cmd/deprecated_test.go` pinning that every deprecated command
  is Hidden and every v0.7 surface command is visible.
- `--help` test expectations updated to match the new visible surface.

## [0.6.0] — 2026-05-07

### Added
- `praxis mcp` (no args) — list every MCP namespace and function the
  gateway exposes, including each function's description and arg shape.
  Use `--json` for AI-host-friendly output. Live fetch from
  `/ai-api/v1/mcp/manifest`.
- `~/.praxis/mcp-tools.json` snapshot — written automatically by
  `praxis install-skill` and `praxis refresh-skills` after the skill
  catalog pull. AI hosts can grep this file for available tool names
  without making a network call. Soft-skipped when not logged in (live
  `praxis mcp` still works as a fallback).
- New `internal/mcpmanifest` package — `Fetch()` and `WriteSnapshot()`
  helpers, separated so both `cmd/mcp.go` (live) and `cmd/skill.go`
  (snapshot) can reuse them.
- `paths.MCPTools()` — canonical location for the snapshot.
- Discovery section added to the praxis meta-skill body, teaching AI
  hosts to use `praxis mcp --json` (live) and the snapshot file (cached).
- Skill execution preamble extended with the same discovery hint, so
  every server-fetched org skill inherits the rule too.

### Changed
- `praxis refresh-skills` now also re-fetches the MCP tool manifest and
  rewrites the snapshot. Pre-0.6 behavior (just rewriting SKILL.md
  files) is preserved when not logged in.

### Server (agent-factory)
- `GET /ai-api/v1/mcp/manifest` — new gateway route, API-key
  authenticated. Returns `{mcps: {<mcp>: {<fn>: {description, args}}}}`
  by introspecting registered dispatchers.
- Each dispatcher now exposes `describe()` returning per-function
  `FunctionSpec` (description + `ArgSpec` list). Names mirror the
  in-process MCP exactly so seeded skill content works verbatim.

## [0.5.0] — 2026-05-07

### Added
- `praxis mcp <mcp> <fn>` — invoke a server-side MCP tool function via
  the agent-factory CLI gateway. The CLI never holds AWS / kube /
  terraform credentials; the server resolves the org from the active
  profile's bearer token and runs the call under org-managed integration
  credentials. Supports `--arg key=value` (repeatable), `--body '<json>'`
  (or `--body -` for stdin), `--json`, `--timeout`. HTTP status →
  exit-code mapping: 401/403 → Auth(3), 404 → NoConfig(4), 5xx → Network(5),
  HTTP 200 + `{isError: true}` → Error(1).
- Skill catalog auto-install — `praxis install-skill` now (also) pulls
  the org skill catalog from `/ai-api/v1/skills/bundle` and installs each
  skill as `praxis-<name>` into every detected AI host. Skills are
  namespaced by prefix so they never collide with user-authored or
  third-party skills. Soft-skips with a hint when not logged in.
- New `internal/skillcatalog` package + `Skill.RenderedContent()` that
  prepends an execution preamble to each installed skill, teaching
  Claude the rewrite rule (`<mcp>.<fn>(...)` → `praxis mcp <mcp> <fn>`)
  without modifying the skill body. Inserted after YAML frontmatter so
  Claude's normal frontmatter discovery still works.

### Changed
- **Active-profile priority** — `~/.praxis/config.json` (set by
  `praxis use`) now beats `PRAXIS_PROFILE` env var. New chain:
  `--profile` flag > `praxis use` > `PRAXIS_PROFILE` > `default`.
  Rationale: `praxis use X` is an explicit, persistent choice — it
  shouldn't be silently overridden by an env var.
- **Removed `--profile` flag** from USE-style commands (`whoami`,
  `mcp`, `status`, `init`, `install-skill`). Active profile is
  resolved exclusively via the chain above. The flag is **kept on
  `login` and `logout`** because there it's the *target* profile name
  (a different semantic).

### Hardening
- `internal/credentials`: new `validateProfileName` regex rejects
  names that would corrupt the INI store (whitespace, control chars,
  `[`, `]`, `=`, `\n`, leading `.`). Wired into `Put`, `Delete`,
  `SetActive`.
- `cmd/login.go`: `url.Parse` errors handled explicitly — no more
  nil-pointer panic on malformed `--url`. New `cmd/login_test.go`
  covers the malformed-URL path.
- `cmd/init.go`: surface `ResolveActive` errors instead of swallowing
  them as "logged out".
- `cmd/skill.go`: catalog-install failures return through cobra `RunE`
  instead of `os.Exit` mid-function.
- Routine cleanup: drop dead imports in `cmd/whoami.go`, errcheck-clean
  `defer resp.Body.Close()` in `internal/skillcatalog`, and assert
  `DeleteAll` actually removes both credentials + config files in
  `credentials_test.go`.

## [0.4.0] — 2026-05-06

### Added
- `praxis login` — browser-callback authentication. Opens the user's
  default browser to create an API key, captures the new key via a
  one-shot localhost listener, validates against `/ai-api/auth/me`,
  saves credentials. Supports `--profile`, `--url`, `--token`,
  `--timeout` flags.
- `praxis whoami` — live identity check against `/ai-api/auth/me`.
- `praxis status` — read-only local snapshot (active profile, URL,
  login state, installed skills) — designed for AI hosts to inspect.
- `praxis init` — idempotent first-run bootstrap: installs the praxis
  skill into all detected AI hosts and reports state as JSON.
- `praxis use <profile>` — kubectl-style: persist active profile to
  `~/.praxis/config.json`.
- Multi-profile credentials store at `~/.praxis/credentials` in INI
  format matching `~/.facets/credentials`. Profile resolution
  priority: `--profile` flag > `PRAXIS_PROFILE` env > `~/.praxis/config.json`
  active-profile pointer > literal `default` section. Single-profile
  users see no behavior change.
- Browser-callback flow on the agent-factory side
  (PR Facets-cloud/agent-factory#1048): the API-key create modal
  reads `cli_callback`, `cli_session`, `suggested_name` query params,
  auto-opens with the name pre-filled, POSTs the new key to the
  localhost listener after creation, redirects to `/ui/ai/cli-success`.
- Named exit codes (`internal/exitcode`): 3 = auth missing/expired,
  4 = no config, 5 = network, 6 = no AI host. Pinned in tests.
- Structured `--json` output mode + auto-non-TTY default
  (`internal/render`).
- Skill content rewrite — the placeholder `praxis` skill now teaches
  AI hosts how to operate the CLI (always `--json`, run `praxis login`
  yourself, exit-code semantics, etc.).

### Changed (breaking)
- `~/.praxis/credentials` format changed from a single JSON object to
  an INI profile map. Existing v0.3 single-credential files are not
  auto-migrated; re-run `praxis login`.
- `praxis logout` now takes `--profile X` (default: active profile)
  or `--all` (wipe everything + active-profile pointer).

### Removed
- `internal/config` package. URL is now stored per-profile inside
  `~/.praxis/credentials`. The legacy `~/.praxis/config.json` JSON
  blob (if any) is ignored; `~/.praxis/config.json` is now the
  active-profile pointer file (INI-formatted, despite the suffix).

## [0.3.0] — 2026-05-06

### Added
- `praxis refresh-skills` — rewrite every installed SKILL.md with the
  current binary's catalog content. Useful after manual edits to
  installed files, or to confirm nothing has drifted.
- `praxis update` now automatically calls the refresh after a successful
  binary replace. Best-effort: a refresh failure does not roll back the
  binary update, only logs a warning.
- `internal/skillinstall.Refresh()` for programmatic use; entries
  pointing at skills the catalog no longer knows about are skipped (not
  removed) so the file can be repopulated by a future catalog version.

## [0.2.0] — 2026-05-06

### Changed (breaking)
- Flattened the `praxis skill *` subtree to top-level commands:
  - `praxis skill install`        →  `praxis install-skill`
  - `praxis skill uninstall`      →  `praxis uninstall-skill`
  - `praxis skill list-installed` →  `praxis list-skills`
- The `[name]` argument has been removed from install/uninstall
  (v0.1's optional arg). Until the server-driven catalog lands, only
  the placeholder "praxis" skill exists, so no name is needed.
- The `praxis skill` parent command no longer exists.

## [0.1.0] — 2026-05-06

### Added
- `praxis skill install` — write a SKILL.md (Agent Skills open standard
  format) into every detected AI host's user-scope skill directory.
  Targets Claude Code (`~/.claude/skills/`), Codex (`~/.agents/skills/`),
  and Gemini CLI (`~/.gemini/skills/`). Cursor is intentionally
  excluded — it has no user-scope skill directory.
- `praxis skill uninstall` — remove the skill from every host where
  it's installed.
- `praxis skill list-installed` — show installed skills and their paths.
- `internal/harness` (re-introduced) — detection for the 3 user-scope
  harnesses.
- `internal/skillinstall` — install/uninstall/list logic + JSON
  receipt at `~/.praxis/installed.json` (atomic writes).
- v0.1 ships with one placeholder skill named `praxis` so the
  multi-harness install machinery is provable end-to-end. The
  server-driven catalog lands in v0.2 once the gateway is shipped.

## [0.0.2] — 2026-05-05

### Removed
- `praxis login`, `praxis whoami`, `praxis skill *` (stubs),
  `praxis mcp *`, `praxis doctor` — these were placeholder cobra
  wirings printing "not yet implemented" and were removed until their
  actual implementations land.
- `internal/harness` package (later reintroduced in v0.1.0).

### Changed
- `internal/paths` trimmed to only the helpers used by current
  commands (`Dir`, `Credentials`).
- Cobra-level unit tests added for every shipped command.

## [0.0.1] — 2026-05-05

### Added
- Initial release: install + version + self-update plumbing only.
- `praxis version` with build-time stamping (version, commit, date).
- `praxis update` — self-update via GitHub Releases with SHA256
  verification and atomic binary replacement.
- `praxis completion {bash|zsh|fish|powershell}` — shell completions.
- `praxis logout` — removes `~/.praxis/credentials`.
- Multi-arch release pipeline (darwin/linux × amd64/arm64) via
  goreleaser, plus auto-publish of the brew formula to
  `Facets-cloud/homebrew-tap`.
