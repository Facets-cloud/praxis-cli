# Changelog

All notable changes to praxis CLI are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

(Empty — see 0.2.0 below.)

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
