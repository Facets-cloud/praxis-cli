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

## Design principle — single-profile users first

The typical praxis user has ONE profile (a customer on one control plane);
everything must resolve silently to "default" for them. Multi-profile users
(Facets engineers, support) are power users: their flows must work, but they
are strictly secondary and must never regress the single-profile flow.
Concretely:

- New flags and subcommands are opt-in; the default flow never grows
  required steps, prompts, or warnings a single-profile user would see.
- Power-user affordances (profile pins, raptor cross-checks, per-directory
  profiles) live behind flags or status fields that stay inert when only
  one profile exists.
- When a trade-off pits multi-profile ergonomics against single-profile
  cleanliness, single-profile wins.

## Match this repo, and keep it small

Before adding anything, open 2-3 existing files that do the same job and copy
their shape. A diff that reads like a different person wrote it is a review cost
even when the logic is right.

- **Smallest diff that works.** Fewest files, fewest new packages. Extending an
  existing file beats a new one; a new one beats a new package.
- **Comments: match the neighbours, which are terse.** One line in a function
  body. Package/function docs 3-8 lines, one long block per file at most. Never
  explain what the code says, never defend a design — that goes in the PR body.
- **No layer for data we already have.** Check what the server already sends and
  what the CLI already parses before writing a parser.
- **No speculative entries.** A filter list, config key or flag with nothing
  using it today is dead state; leave it out.

Measure, don't guess: `grep -cE '^\s*//' <file>`, and prefer files whose
`git log` has no `Co-Authored-By: Claude` — that is a real baseline, not prior
AI output.

## Testing — non-negotiable

**Unit test coverage is required, not optional.**

- Every new package must have a `*_test.go` alongside it from the first
  commit that introduces it. No "I'll add tests later" — `later` doesn't
  come.
- Bug fixes land with a regression test that fails before the fix and
  passes after.
- `make test` (= `go test -race ./...`) must stay green on every commit
  to `main`. CI gates merges on this.
- Never lower a package's coverage. Use `go test -cover ./...` before
  opening a PR. This includes `cmd/*` cobra commands — test them with
  `cmd.SetOut(&buf)` and call `RunE`/`Run` directly. Mock external
  dependencies via package-level function vars (see `cmd/update.go`'s
  seams as a reference).

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
  duty.go             `praxis duty *` (Agent Schedule runs/findings/reports)
  hook.go             `praxis hook user-prompt-submit` (hidden) — the AI-host
                       prompt hook that nudges toward a matching praxis skill
internal/             pure logic, unit-tested
  paths/              Praxis filesystem locations. Two roots: the HOME root
                       (~/.praxis, always holds credentials + global pointer)
                       and a discovered PROJECT root (<repo>/.praxis) that
                       becomes ActiveRoot for the receipt/snapshot/skills.
  duties/             REST client for Agent Schedules (duties): runs,
                       findings, artifacts — mirrors internal/memory
  raptorstate/        mirror of raptor's profile resolution
                       (~/.facets/credentials + FACETS_* env) so `status`
                       can cross-check raptor's control plane against the
                       praxis profile URL. Read-only except SeedProfile,
                       which ADDS an absent section and never edits or
                       removes an existing one
  selfupdate/         GitHub Releases fetch, checksum, atomic replace
  hosthooks/          merges praxis's hooks into each AI host's hook config.
                       ONE JSON merge engine, per-host differences in a Host
                       spec — file path, event key, and timeout UNIT all
                       differ (see the invariants below)
  skillnudge/         keyword index built from installed skills' frontmatter
                       `triggers:` + a small static Facets vocabulary; decides
                       which skill a prompt should invoke
Makefile              build (with ldflags), install, test, lint, clean
.goreleaser.yml       release config — raw binaries × 4 arches + brew tap
.github/workflows/    ci.yml (every push), release.yml (on tag)
```

**Don't add stub commands.** A cobra command that prints "not yet
implemented" is worse than no command — it lies to users and to
`--help`. Skill sourcing and the server gateway are now live:
`login`, `logout`, `status`, `profiles`, `profiles use`, `mcp`,
`list-skills`, and `refresh-skills` are all implemented (skills install
automatically as part of `login`/`profiles use`/`refresh-skills`). Skills are
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
- **Seeding raptor's store only ever ADDS, and is gated on the CREDENTIAL.**
  `raptorstate.SeedProfile` hands a verified control-plane PAT to raptor,
  because otherwise a successful login leaves `raptor whoami` failing and the
  only documented recovery (`raptor login`) needs a TTY an AI host can't give
  it. Three things to keep:
  - It writes only an ABSENT section — an existing one may hold a credential
    for another control plane. The write is atomic (temp + rename), because a
    partial write truncates raptor's OTHER profiles, which is the loss the
    never-overwrite rule exists to prevent.
  - The call sits in `persistAndSetup`, the tail EVERY tier converges on, and
    self-gates on `prof.AuthMode == AuthModeBasic`. Gating by call site instead
    is how the first version shipped a hole: tier (a) reuses a stored CP PAT
    without re-prompting, so "re-run praxis login" never restored raptor. A
    Praxis API key is excluded by the field, not by where the call sits.
  - The section name comes from `seedTarget`, which mirrors `resolve()`'s order
    (pin > `FACETS_PROFILE` > `default`). Writing `[default]` while a
    `FACETS_PROFILE` shell reads `[acme]` leaves raptor broken by exactly the
    amount this fixes.
- **A bare or foreign `.praxis` must stay inert.** Local mode activates
  only via `LocalModeActive` (pointer names a known profile). Don't switch
  any state on mere directory presence — that's what protects a user who
  never opted in (a teammate-committed `.praxis` resolves to the global
  profile *and* keeps receipt/snapshot/skills global).
- **Receipt and install location share one root.** Both derive from
  `ActiveRoot()`, so the unconditional "wipe previous profile" step
  (`UninstallByPrefix`) only ever touches the active root. Callers set the
  scope up front by pinning via `paths.OverrideActiveRoot` (login --local /
  `profiles use --local` / refresh --project) or by being in an active local
  tree; never make a scope decision that diverges receipt from install.
- **Moving the pointer and re-installing skills is one operation.** Both
  callers that change the active profile (`praxis login`, `praxis profiles
  use`) go through `cmd.activateProfile`, which flips the pointer AND pins
  the matching `ActiveRoot` in the same step, then run `postAuthSetup`.
  Never flip a pointer without the re-sync: that's how you get profile A
  active with profile B's `praxis-*` skills on disk. `profiles use`
  additionally verifies the stored token via `/auth/me` BEFORE any write,
  so a dead token or an unreachable server changes nothing (exit 3 / 5).
- **`login` (global), `logout`, and `profiles use` (no --local) are global
  by design.** They pin the active root to home
  (`paths.OverrideActiveRoot(home)`) and — for login/logout — resolve the
  global profile (`credentials.ResolveActiveGlobal`), so being inside a
  project tree never redirects them. A global `profiles use` run inside a
  local-mode tree reports `shadowed_by_project_root` +
  `effective_profile`, since the project pointer still wins there.
- **Active-profile resolution** (`credentials.resolveName`): `--profile`
  flag → `$PRAXIS_PROFILE` → project pointer → global pointer →
  `"default"`. `SourceProject` marks the project case; a project pointer to
  an unknown profile falls back to the global resolution, but an unknown
  flag/env profile does NOT — an explicit choice must fail loudly (exit 3)
  rather than silently route to a different org.
- **`$PRAXIS_PROFILE` is the concurrency-safe scope**
  (`credentials.EnvProfile`). Both the active-profile pointer and the
  installed `praxis-*` skills are machine-global, so `profiles use` repoints
  every other shell/agent session on the box AND rewrites skill files those
  sessions have already read. The env var lives in the process environment:
  it writes nothing and is unobservable from another session. It MUST
  outrank the project pointer, or a pinned repo couldn't be scoped per
  session. Residual limitation — documented, not fixed: skill FILES still
  belong to the globally-active profile, so `-p`/env give the right gateway
  with the active profile's skill text.
- **`--profile` is ONE flag on `rootCmd`**, persistent, shorthand `-p`,
  bound to `cmd.rootProfile` (root.go). Never define a local `--profile` on
  a subcommand: cobra lets the local flag shadow the inherited one, so
  `praxis login -p x` and `praxis -p x login` would land in different
  variables and one of them would be silently ignored. Commands read it via
  `activeOrAuthExit` (memory/duty/ig/agents) or by passing `rootProfile` to
  `credentials.ResolveActive`. Tests MUST reset `rootProfile` — it's package
  state shared by every command (`resetLoginFlags`, `resetIgFlags`,
  `setRootProfile` all do).
- **A command that can't honor `--profile` must REFUSE it**, never ignore
  it: `refusedProfileFlag` / `refusedExplicitProfile` (root.go) print a usage
  error and exit 2. Today that's `logout` and `refresh-skills` (both rewrite
  the ACTIVE profile's org skills, so honoring the flag would split pointer
  from skills) and `profiles use` (target is positional). For `logout` the
  check MUST come before the `--all` branch — `-p X --all` is a
  contradiction, and ignoring `-p` there wipes every profile for a user who
  named one.
- **Refuse on DIVERGENCE, not on presence.** Both helpers take the profile
  the command acts on and stay silent when the selection names it: `praxis -p
  default logout` on a single-profile machine asks for exactly what a bare
  `logout` does. The comparison target MUST be resolved without the flag and
  the environment, or it compares the selection with itself and never fires —
  `credentials.PersistedActiveName()` for `logout` (global by design) and
  `credentials.PointerActiveName()` for `refresh-skills` (project pointer
  first, matching the root it installs into).
- **A guard and its action MUST be one decision.** Resolve the target once and
  have the action use that same name; never let the action re-resolve through
  `ResolveActive*`, or the two disagree about "which profile?" and the action's
  answer wins. This shipped as a destructive bug: the guard compared the
  pointer while `logout` deleted `ResolveActiveGlobal()`, so `-p default
  logout` under `PRAXIS_PROFILE=acme` passed the check and deleted **acme**.
  Two defenses, keep both — `refusedExplicitProfile` checks the flag and the
  environment INDEPENDENTLY (not the flag-wins winner, which a matching `-p`
  satisfies while the env still diverges), and the action reuses the approved
  name (`target` in logout, `ResolveActive(acts)` in refresh-skills).
- **Divergence-only refusal made previously-unreachable states reachable.**
  The blanket refusal masked every guard/action mismatch behind it. When
  loosening a guard, audit what the action does with a selection the guard now
  admits — the bug arrives with the fix, not before it.
- **Multi-profile guidance is gated on the profile count.** The single-profile
  customer gets no precedence chain, no machine-global warnings and no
  refusal table — in the meta-skill (`skillinstall.MultiProfileMachine`, a
  seam wired in `cmd.init` because skillinstall must not read the credentials
  store) or in `profiles use` output (`switchSummary.MultiProfile`, from the
  store the command already loaded). It describes a choice they don't have,
  and it's what teaches a host to pass `-p` at the only profile there is.
- Discovery is **home-subtree only** — matches the intended use case and
  keeps tests deterministic under a faked `$HOME`. Tests drive discovery
  via `paths.SetGetwdForTest` and pin via `paths.OverrideActiveRoot`.
- **Compare `$HOME` and the cwd in one namespace.** `$HOME` can be logical
  while `os.Getwd()` reports the physical path (macOS `/tmp` is a symlink to
  `/private/tmp`), so a plain prefix test wrongly calls the cwd "outside
  home" and silently disables local mode everywhere. `paths.alignUnder`
  handles it: literal compare first (no syscalls, keeps the user's
  spelling), then both sides `EvalSymlinks`'d, returning the pair from
  whichever namespace matched — the walk-up bound only stops at home when
  both come from the same one. Resolution only ADDS matches, so a symlink
  pointing out of home keeps working. Don't reintroduce a bare
  `isUnder(home, cwd)` on those two.

## AI-host hooks (the skill nudge)

`praxis login` wires a prompt-submit hook into each host that has one; `logout`
removes it. The command written into a host config must be a path that SURVIVES
an upgrade (`claudehooks.BinaryPath`): Homebrew stages the binary under
`Caskroom/<version>/`, so a resolved path dies at the next upgrade. `praxis
setup` and `praxis update` call `claudehooks.Repair` to heal hooks wired that
way — it re-points existing entries only, and only from a PATH entry that IS the
running binary, so a throwaway build cannot capture a real install's hooks.

Per-host differences that are NOT guessable: Gemini's event is
`BeforeAgent` (not `UserPromptSubmit`) and its timeout is in MILLISECONDS;
Codex needs an in-app `/hooks` trust before it runs anything; Antigravity has no
hook mechanism. See the `Hosts()` table.

### Two paths to the same binary — do not mix them up

`praxis update` and the hook writer need OPPOSITE answers, and each is wrong for
the other:

- A **hook command** must name the DURABLE path (`claudehooks.BinaryPath` — the
  PATH entry / brew's `bin/praxis` symlink). A resolved path names a
  version directory that the next upgrade deletes.
- A **file replacement** must name the REAL file (`selfupdate.TargetPath`).
  `os.Rename` does not follow a destination symlink — it REPLACES it — so
  renaming over brew's `bin/praxis` turns that link into a regular file and
  Homebrew can no longer upgrade or uninstall (`brew upgrade` then fails with
  "already a Binary at …", and its rollback can purge the Caskroom entry).

`praxis update` additionally REFUSES a Homebrew-managed install
(`selfupdate.HomebrewCask`) rather than write into brew's tree: brew records the
version in its own metadata, so a self-update there makes `brew info` report a
version that is not on disk. This exits 0 with `reason: homebrew_managed` — it
is guidance, not a failure.

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
Releases.

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
