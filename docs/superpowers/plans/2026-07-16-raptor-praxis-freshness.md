# Tool freshness (praxis + raptor) — Implementation Plan

> **For agentic workers:** implement task-by-task (TDD). Steps use `- [ ]` checkboxes.

**Goal:** praxis keeps the user aware when **raptor** *or praxis itself* is behind
its latest release, and nudges toward an upgrade — through ONE unified freshness
engine, not a raptor path bolted beside the existing praxis self-update path.

**Architecture:** Generalize the existing praxis update-check
(`cmd/update_check.go`) into a small **multi-tool freshness engine** driven by a
tool registry. praxis becomes one entry, raptor another. Everything shared —
`compareSemver`, the 24h file cache, fetch-with-retry, the nag renderer, the
throttle, the skip-list. praxis's own self-update nag is **migrated onto this
engine** (no separate path). Four surfaces read the same engine for both tools:
`praxis status --json`, `praxis login`, the Execute-time TTY nag, and the
driver/raptor skill copy. Nudge-only: praxis never runs `raptor upgrade`.

**Tech stack:** Go 1.24, cobra. Reuse `cmd/update_check.go` +
`internal/selfupdate`. NO new duplicate semver/cache/render code.

## Global Constraints

- **Unify, don't fragment.** One engine, one cache file, one comparator, one
  renderer, one throttle. praxis and raptor differ ONLY in their `toolSpec`
  (current-version resolver, releases repo, applicability gate, upgrade hint).
  praxis's existing nag MUST route through the generalized engine, not stay
  parallel.
- **Best-effort, never fatal.** Any network/exec/parse error → "not stale",
  Latest=""; never block a command, never nag on uncertainty.
- **Nudge-only for raptor.** praxis MUST NOT run `raptor upgrade`. It surfaces
  staleness; the agent asks the user, then runs it (raptor's "ask before
  upgrading"). `praxis update` remains praxis-only (self-replaces its binary).
- **Preserve existing praxis behavior exactly:** `isDevBuild` skip,
  `PRAXIS_NO_UPDATE_CHECK` kill-switch, 24h throttle, TTY-stderr nag, 3s maxWait,
  `skipUpdateCheck` list, box format. Existing update-check tests stay green.
- **Throttle:** ≤ one GitHub call per tool per 24h; cache 0600 under `~/.praxis`.
- **raptor:** version = first `\d+\.\d+\.\d+` from `raptor --version`
  (`raptor version 0.1.81` locally); absent (`command -v raptor` empty) →
  Installed=false, never stale. Latest = newest tag from public
  `Facets-cloud/raptor-releases`.

---

## Task 1: Parameterize latest-release fetch by repo

**Files:** `internal/selfupdate/selfupdate.go`, `internal/selfupdate/selfupdate_test.go`

`latestReleaseURL()` hardcodes praxis-cli. Add `LatestReleaseTagFor(repo string)
(string, error)` (reuses `fetchRelease`) returning just the tag. `LatestRelease()`
keeps returning the full `*Release` (assets needed by `praxis update`) but is
re-expressed over the shared URL builder so there is one code path.

- [ ] Test: `LatestReleaseTagFor("owner/repo")` vs httptest → tag; 404/500/bad-JSON → error.
- [ ] Run `go test ./internal/selfupdate/ -run LatestReleaseTagFor` → FAIL → implement → PASS. Commit.

## Task 2: Generalize the engine to a tool registry (the core refactor)

**Files:** `cmd/update_check.go`, `cmd/update_check_test.go`

The refactor that prevents fragmentation. Introduce:

```go
type toolSpec struct {
    Name         string
    Current      func() (version string, applicable bool) // praxis: (version, !isDevBuild); raptor: parse (v, installed)
    Repo         string                                    // "Facets-cloud/praxis-cli" | "Facets-cloud/raptor-releases"
    UpgradeHint  string                                    // "praxis update" | "raptor upgrade"
}

type Freshness struct {
    Tool, Current, Latest string
    Installed, Stale      bool
}

func tools() []toolSpec { return []toolSpec{praxisSpec(), raptorSpec()} }
func checkTool(spec toolSpec, now time.Time) Freshness  // uses the SHARED cache + fetch + compareSemver
```

- Generalize the cache: `updateCheckCache{CheckedAt, LatestVersion}` →
  `map[string]toolCacheEntry` keyed by tool name (one file, both tools). An old
  single-tool cache simply fails to unmarshal → treated as cold (harmless).
- `fetchLatestReleaseWithRetry` takes the repo (via Task 1). The praxis
  `fetchLatestRelease` seam is preserved for existing tests.
- **Migrate praxis:** rewrite `checkForUpdate()` as `checkTool(praxisSpec(), now)`
  → nag tag. praxisSpec().Current returns `(version, !isDevBuild(version))`.
- raptorSpec().Current: `command -v raptor` then parse `raptor --version`.
- Keep `compareSemver`/`splitVersion`/`newerThan` as-is (already shared).

- [ ] Test: praxis path unchanged — dev build → applicable=false → no nag; older current + newer latest (cached) → nag tag; kill-switch honored.
- [ ] Test: `checkTool(raptorSpec)` with injected latest > current → Stale; absent raptor → Installed=false; fetch error → not stale.
- [ ] Test: cache keyed per tool — a fresh praxis entry doesn't suppress a raptor fetch and vice-versa; 24h TTL respected.
- [ ] Run → FAIL → refactor → PASS (all pre-existing update-check tests still green). Commit.

## Task 3: Execute-time nag over ALL tools

**Files:** `cmd/root.go`, `cmd/update_check.go`, tests

Generalize `printUpdateNotification(latest)` → `printToolNotification(Freshness)`
(parameterized name / current→latest / hint line: "Run praxis update" vs "Ask me
to run raptor upgrade"). Execute() loops the tool registry, runs the shared check
in the existing background/TTY/maxWait pattern, prints one box per stale tool.
`skipUpdateCheck` and the kill-switch unchanged.

- [ ] Test: `printToolNotification` renders praxis box identical to today (golden), and a raptor box with the `raptor upgrade` hint.
- [ ] Test: Execute nag path emits raptor box when raptor stale (injected), nothing when fresh, skipped non-TTY.
- [ ] Run → FAIL → implement → PASS. Commit.

## Task 4: `praxis status --json` freshness block

**Files:** `cmd/status.go`, `cmd/status_test.go`

Add `"tools": [Freshness{praxis}, Freshness{raptor}]` to the status snapshot from
the SAME engine, CACHED (no network on a plain status). `status --refresh` forces
a live re-check alongside the token check.

- [ ] Test: `status --json` includes `tools` with both entries (injected engine); `--refresh` triggers a live check.
- [ ] Run → FAIL → implement → PASS. Commit.

## Task 5: Login-time stale notice

**Files:** `cmd/login_setup.go`, tests

After post-auth setup, one line per stale tool via the shared renderer (non-JSON);
JSON login envelope gains `tools`. Best-effort, non-fatal.

- [ ] Test: stale injected tool prints a notice; fresh prints nothing; JSON carries `tools`.
- [ ] Run → FAIL → implement → PASS. Commit.

## Task 6: Driver skill + raptor preamble freshness step

**Files:** `internal/skillinstall/dummy.go`, `internal/render/preamble.go`, guard tests

Short step: "Glance at `praxis status --json` → `tools`. If `raptor.stale` (or
praxis's own), tell the user and offer to run `raptor upgrade` (ask first — never
auto-run). praxis surfaces versions; you + the user decide."

- [ ] Test (embedded guard): copy mentions raptor freshness + `raptor upgrade` + "ask".
- [ ] Run → FAIL → add copy → PASS. Commit.

## Self-review checklist

- praxis's own nag routes through the generalized engine (no parallel path left).
- One cache file, one comparator, one renderer, one throttle — praxis/raptor differ only in `toolSpec`.
- All four surfaces read the same engine, both tools.
- Best-effort everywhere; no path blocks a command; no code runs `raptor upgrade`.
- Every pre-existing update-check test still passes.

## Sequencing / release

New branch off main (post-#61), own PR → `v1.4.4`.
