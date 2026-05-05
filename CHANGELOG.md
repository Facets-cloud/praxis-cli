# Changelog

All notable changes to praxis CLI are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Removed
- `praxis login`, `praxis whoami`, `praxis skill *`, `praxis mcp *`,
  `praxis doctor` — these were placeholder cobra wirings printing
  "not yet implemented" and are removed until their actual
  implementations land. Listing them in `--help` was misleading. The
  cobra surface now only advertises commands that actually do
  something.
- `internal/harness` package (only consumer was `praxis doctor`;
  Phase 2's skill installer will reintroduce it when needed).

### Changed
- `internal/paths` trimmed to only the helpers used by current
  commands (`Dir`, `Credentials`).

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
