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
- Coverage target: **≥ 75%** for `internal/*` packages (use
  `go test -cover ./internal/...` before opening a PR). Cobra command
  files in `cmd/*` are exempt — they're best covered by end-to-end
  binary tests, not unit tests.

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
cmd/                  cobra command tree
  root.go             root cmd, version vars (ldflags-injected)
  version.go          `praxis version` subcommand
  doctor.go           `praxis doctor`
  update.go           `praxis update` (self-update via GitHub Releases)
  completion.go       `praxis completion {bash|zsh|fish|powershell}`
  auth.go             login (stub), logout, whoami (stub)
  skill.go            skill {list|show|install|uninstall|...} (stubs)
  mcp.go              universal `mcp <mcp> <fn> [--arg val ...]`
  helpers.go          shared helpers (notImplemented stub)
internal/             pure logic, fully unit-tested
  paths/              ~/.praxis filesystem locations
  harness/            AI host detection (Claude Code, Cursor, Gemini CLI)
  selfupdate/         GitHub Releases fetch, checksum, atomic replace
Makefile              build (with ldflags), install, test, lint, clean
.goreleaser.yml       release config — raw binaries × 4 arches + brew tap
.github/workflows/    ci.yml (every push), release.yml (on tag)
```

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

## License

MIT. See [LICENSE](LICENSE).
