# Changelog

All notable changes to praxis CLI are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

(Empty — see 0.4.0 below.)

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
