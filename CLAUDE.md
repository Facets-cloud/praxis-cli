# CLAUDE.md — praxis-cli

Guidance for Claude Code (and other AI assistants) working in this repo.
Project-specific overrides for the global `~/.claude/CLAUDE.md`.

## Project overview

Single-binary Go CLI (`praxis`) that exposes Praxis cloud capabilities to
any local AI host (Claude Code, Cursor, Gemini CLI). The CLI is a thin
HTTPS client to a Praxis cloud deployment — it does not run an agent
loop locally. Skills are sourced (fetched + nomenclature-translated) into
the user's AI host; MCP tools execute server-side under org-managed
credentials. See [README.md](README.md) for the user-facing story.

## Testing — non-negotiable

**Unit test coverage is required, not optional.**

- Every new package must have a `*_test.go` alongside it from the first
  commit that introduces it. No "I'll add tests later" — `later` doesn't
  come.
- Bug fixes land with a regression test that fails before the fix and
  passes after.
- `make test` (= `go test -race ./...`) must stay green on every commit
  to `main`. CI gates merges on this.
- Coverage target: **≥ 75%** across the board. Use
  `go test -cover ./...` before opening a PR. This includes `cmd/*`
  cobra commands — test them with `cmd.SetOut(&buf)` and call
  `RunE`/`Run` directly. Mock external dependencies via package-level
  function vars (see `cmd/update.go`'s seams as a reference).

### Conventions

- Table-driven tests are the default (`tests := []struct{ name, in, want }{...}`).
- HTTP code uses `net/http/httptest.Server` for stubbing — never hit
  the real network from a test.
- Filesystem code uses `t.TempDir()` for isolation — never write to
  `~/.praxis` from a test.
- Error paths matter: assert on the *type* / contents of returned
  errors, not just `err != nil`.

## Project structure

```
main.go               entrypoint — calls cmd.Execute()
cmd/                  cobra command tree (only commands that DO something
                       — no stubs; later phases add commands when their
                       implementation lands)
  root.go             root cmd, version vars (ldflags-injected)
  version.go          `praxis version`
  update.go           `praxis update` (self-update via GitHub Releases)
  completion.go       `praxis completion {bash|zsh|fish|powershell}`
  logout.go           `praxis logout` (deletes ~/.praxis/credentials)
  use.go              `praxis use <profile>` (sets the GLOBAL active-profile
                       pointer; local mode is `praxis login --local`)
  duty.go             `praxis duty *` (Agent Schedule runs/findings/reports)
internal/             pure logic, unit-tested
  paths/              Praxis filesystem locations. Two roots: the HOME root
                       (~/.praxis, always holds credentials + global pointer)
                       and a discovered PROJECT root (<repo>/.praxis) that
                       becomes ActiveRoot for the receipt/snapshot/skills.
  duties/             REST client for Agent Schedules (duties): runs,
                       findings, artifacts — mirrors internal/memory
  selfupdate/         GitHub Releases fetch, checksum, atomic replace
Makefile              build (with ldflags), install, test, lint, clean
.goreleaser.yml       release config — raw binaries × 4 arches + brew tap
.github/workflows/    ci.yml (every push), release.yml (on tag)
```

**Don't add stub commands.** A cobra command that prints "not yet
implemented" is worse than no command — it lies to users and to
`--help`. Skill sourcing and the server gateway are now live:
`login`, `whoami`, `mcp`, `install-skill`, `uninstall-skill`,
`list-skills`, and `refresh-skills` are all implemented. Skills are
fetched from the server, name-prefixed (`praxis-*`), and have the
`render.ExecutionPreamble` inserted after their frontmatter so any
in-process MCP reference (`run_cloud_cli(...)`) is rewritten to a
`praxis mcp <mcp> <fn> --arg …` shell-out — see
`internal/skillcatalog` and `internal/render/preamble.go`.

## Local mode (per-directory profiles)

`praxis login --profile X --local` pins a profile to the current
directory tree: it writes a project pointer at `<cwd>/.praxis/config.json`
(leaving the global pointer alone) and installs project-scoped.
`refresh-skills --project` does the same for the already-active profile,
minus auth. A `.praxis/` directory is discovered git-style by
`paths.ProjectRoot()` (walking up from cwd, **bounded to `$HOME`**).

The active root (`paths.ActiveRoot()`) decides where the skill receipt
(`installed.json`), MCP snapshot (`mcp-tools.json`), and installed skills
live. It returns the discovered project root **only when local mode is
genuinely active** — gated by the `paths.LocalModeActive` hook, which the
credentials package wires up to check that the project pointer names a
profile actually present in the store. Otherwise it returns the HOME root.

Invariants to preserve when touching this area:

- **Credentials are always global.** `paths.Credentials()` is pinned to
  the HOME root; never route it through `ActiveRoot()`.
- **A bare or foreign `.praxis` must stay inert.** Local mode activates
  only via `LocalModeActive` (pointer names a known profile). Don't switch
  any state on mere directory presence — that's what protects a user who
  never opted in (a teammate-committed `.praxis` resolves to the global
  profile *and* keeps receipt/snapshot/skills global).
- **Receipt and install location share one root.** Both derive from
  `ActiveRoot()`, so the unconditional "wipe previous profile" step
  (`UninstallByPrefix`) only ever touches the active root. Callers set the
  scope up front by pinning via `paths.OverrideActiveRoot` (login --local /
  refresh --project) or by being in an active local tree; never make a
  scope decision that diverges receipt from install.
- **`login` (global) and `logout` are global by design.** They pin the
  active root to home (`paths.OverrideActiveRoot(home)` / logout) and
  resolve the global profile (`credentials.ResolveActiveGlobal`), so being
  inside a project tree never redirects them.
- **Active-profile resolution** (`credentials.resolveName`): `--profile`
  flag → project pointer → global pointer → `PRAXIS_PROFILE` → `"default"`.
  `SourceProject` marks the project case; a project pointer to an unknown
  profile falls back to the global resolution.
- Discovery is **home-subtree only** — matches the intended use case and
  keeps tests deterministic under a faked `$HOME`. Tests drive discovery
  via `paths.SetGetwdForTest` and pin via `paths.OverrideActiveRoot`.

## Build & run

```bash
make build              # builds ./praxis with version stamp from git
./praxis --help
make test               # go test -race ./...
make lint               # gofmt + vet + test
go test -cover ./...    # coverage report
```

Version is stamped via `-ldflags -X cmd.version=...` (see Makefile).
Override at build time: `make build VERSION=v0.5.0-dev`.

## Adding a new command

1. Create `cmd/<verb>.go` with a cobra command and `init()` that adds it.
2. If it touches a server endpoint, route through `internal/httpclient`
   (Phase 3 will add this); never call `net/http` directly from `cmd/*`.
3. If it has parseable JSON output, support `--json` and auto-emit JSON
   when `os.Stdout` is not a TTY (so AI hosts spawning praxis as a
   subprocess always get parseable output).
4. Write a unit test for any non-trivial logic in a corresponding
   `internal/` package; the cobra binding itself doesn't need a unit
   test, but the logic it calls does.

## Adding a new internal package

1. Create `internal/<name>/<name>.go`.
2. Create `internal/<name>/<name>_test.go` in the same commit.
3. Tests must cover the package's exported API and the main failure
   paths. No exceptions.

## Distribution

Released via Homebrew (`Facets-cloud/homebrew-tap`) and direct GitHub
Releases binary download. `praxis update` self-updates against GitHub
Releases. `install.askpraxis.ai` is a separate shell-script install
path (not yet built).

## Shipping a change (merge → release → upgrade → test)

The end-to-end runbook for getting a merged change into the locally
installed binary. Releases are **tag-driven**: pushing a `v*.*.*` tag
fires `.github/workflows/release.yml`, which runs goreleaser to publish
the GitHub Release and bump the Homebrew cask in `facets-cloud/tap`.
There is no `make release` target.

1. **Wait for review + CI, then merge the PR.** Let CodeRabbit finish
   its pass and address its findings; the `build` and `goreleaser-check`
   checks must be green. Squash-merge to `main`.
2. **Tag the new version on `main`:**
   ```bash
   git checkout main && git pull
   git tag vX.Y.Z          # minor bump for a feature, patch for a fix
   git push origin vX.Y.Z
   ```
   (Current scheme: semver, e.g. `v0.12.0` → `v0.13.0` for a feature.)
3. **Watch the release CI** (`gh run watch` / `gh run list --workflow
   release.yml`). goreleaser publishes the GitHub Release and pushes the
   updated cask to the tap. Needs the `HOMEBREW_TAP_TOKEN` secret.
4. **Upgrade locally** once the cask lands:
   ```bash
   brew update && brew upgrade --cask praxis
   ```
   (Installed at `/opt/homebrew/bin/praxis` from cask `facets-cloud/tap`.)
5. **Test in local** — run `praxis version` to confirm the new version,
   then exercise the shipped change against the real CLI (read-only
   commands are safe to run live).

## License

MIT. See [LICENSE](LICENSE).
