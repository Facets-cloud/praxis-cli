# Changelog

All notable changes to praxis CLI are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial CLI scaffold (Go + cobra)
- `praxis version` with build-time stamping (version, commit, date)
- `praxis doctor` — checks `~/.praxis` writability, credentials presence,
  `PRAXIS_API_URL` reachability, and detects Claude Code / Cursor / Gemini CLI
- `praxis update` — self-update via GitHub Releases with SHA256 verification
  and atomic binary replacement
- `praxis completion {bash|zsh|fish|powershell}` — shell completion scripts
- `praxis logout` — removes `~/.praxis/credentials`
- Stub commands ready for Phase 2 / Phase 3:
  `login`, `whoami`, `skill {list|show|install|uninstall|list-installed|refresh}`,
  `mcp {list|<mcp>|<mcp> <fn> [--arg val …]}`
- Goreleaser config for darwin/linux × amd64/arm64 raw-binary releases
- Homebrew tap formula generation (`Facets-cloud/homebrew-tap`)
