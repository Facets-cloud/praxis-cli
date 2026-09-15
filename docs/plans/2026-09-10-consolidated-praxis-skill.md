# Consolidated Praxis skill implementation

Approved design: CLI-owned single lazy `praxis` package referring to the independent
`raptor` skill for Facets semantics. Implementation starts from CLI 5663237 and
Agent Factory de9d8850 in separate worktrees. No commit, push, deployment, live
cloud operation, or blanket local skill deletion is authorized by this work.

## Global constraints

Follow-up dependency lifecycle (2026-09-10): the user requested installing the
Raptor skill in Praxis's skill setup path, bootstrapping a missing Raptor binary
at login, and internally running `raptor upgrade` during Praxis update/upgrade.
Explicit setup/login/refresh now install missing Raptor without sudo, verify the
published SHA-256 digest, and export its skill in an isolated home before a
transactional host install. Existing valid independent skills/source links stay
untouched. Silent first-use bootstrap remains offline/Praxis-only. Update invokes
Raptor's own upgrader, including already-current/Homebrew-managed Praxis paths;
each tool's outcome is reported separately. Raptor must publish its consolidated
embedded skill before a freshly downloaded release can provide it.

Follow-up decision (2026-09-10): the user requested stopping CLI export in Agent
Factory, explicitly preserving web availability, like the Raptor skills. This
supersedes the opt-in server export/old-client fallback provisions below:
all 30 replaced GLOBALs stop exporting to every CLI. Keep seeds, authored
surfaces, web/pod delivery and scoped skills. Client-side compatibility with
older servers remains; the updated server no longer promises legacy fallback.
The original implementation plan below records the earlier design.

- Canonical package: `internal/skillinstall/embedded/praxis/` in praxis-cli.
- A short SKILL.md routes to task-specific references, scripts, assets. Do not
  load the entire reference tree or require Raptor for non-Facets tasks.
- Preserve 16 remaining AF CLI capabilities and five embedded entrypoints via
  one Praxis entrypoint; 14 Facets globals hand off to verified Raptor.
- Preserve organization/personal skills, custom agents, hosted seed definitions,
  external symlink targets, unrelated roots and modified local skill content.
- Old CLI/server combinations must retain legacy delivery when replacement is
  unavailable. Filtering applies only to exact GLOBAL names, never prefix globs.
- Establish RED before behavior changes, then test in temporary filesystem roots
  and mock HTTP. Do not use live credentials/services in tests.
- No new worktree after these approved worktrees. No commits/pushes.

## Task 1: CLI package lifecycle and routing

Own Go source/tests only (not embedded skill content, authored separately).
Embed the complete `embedded/praxis` tree (including hidden assets). Install only
`praxis` as the default built-in entrypoint; old embedded sources may remain for
legacy compatibility, but must not be reinstalled by default or advertised as
canonical. Refresh and bootstrap must work offline for the replacement.

Add compatible bundle capability negotiation: optional query parameter
`consolidated=praxis-v1,raptor-v1`. Send only capabilities verified present for
the target hosts; `praxis-v1` may be advertised after successful complete Praxis
installation, `raptor-v1` only after inspecting a genuine loadable canonical
Raptor package, not just finding its executable. Missing Raptor retains 14
legacy globals. New clients must also filter the same exact global names locally
when an old server ignores the parameter. Existing `Skill.Scope` supports this.

Praxis replacement global names (16): aws-change-audit, build-web-component,
cache-migrate, cloud-operations, cloud-waste-finder, custom-agents-operations,
db-migrate, duties-operations, k8s-operations, learning, newrelic-operations,
secrets-migrate, onboard-ig, praxis-dag, praxis-dag-runner, slack-progress-tracker.
Raptor replacement names (14): audit-facets-blueprint, build-facets-module,
design-facets-module, docs-helper, facets-blueprint, facets-ci,
facets-gcp-zero-change-import, facets-notifications, module-actions,
modules-repo-workflow, release-debugging, terraform-import, zero-change-import,
facets-module-testing.

Replace destructive skill-prefix wiping in the refresh path with staged,
validated writes and provenance-aware retirement. Receipt metadata should carry
source/scope/digest when known. Preserve/backup uncertain or modified legacy
content outside discovery roots. Exact mapped legacy globals and old built-in
entrypoints may be retired only after replacements are present. Do not touch
org/personal namesakes, untracked arbitrary `praxis-*`, symlink targets, custom
agents or other project roots. Serialize refresh and roll back partial failures;
retain last usable skills on fetch/install failure. Validate portable paths.

Update prompt hook routes to canonical Praxis vs Raptor; ig hook selects
Praxis catalog-read. Preserve additive host hook installation, timeouts and
trust behavior. Fix update's old-process embedded content refresh: the newly
installed binary must supply refreshed content, or explicitly defer to next
setup; do not claim old running embedded bytes are the new skill.

Test actual installation trees, complete files, canonical names, compatible
server matrix, missing Raptor, org/private namesakes, malformed paths, modified
content, symlinks, failed fetch/write, concurrency and receipt preservation.
Use t.TempDir/httptest. Run focused tests, then `go test -race ./...` and coverage.

## Task 2: Skill content and preservation ledger

Own embedded/praxis content and skill evaluation artifacts. Read current sources
before consolidating each capability. Preserve scripts/templates under distinct
subfolders, then validate actual behavior and limitations. Correct stale store,
MCP envelope, duty run, ig builder, cloud mutation, and migration claims verified
against latest source. Keep task packets/artifact provenance, UI transport/theme,
memory visibility, scoped repository access, and optional learning/onboarding.
Use Raptor for config, outputs, modules, imports, delivery, release/action and UI
registration. Record source-to-reference map, differences and test scenarios
outside the shipped skill tree. Run independent before/after reader evaluation,
frontmatter/link/package checks, helper smoke/unit tests without cloud writes.

## Task 3: Compatible server export and hosted discoverability

Own Agent Factory source/tests. Support optional `consolidated` query from Task1.
Only exact GLOBAL replacements are omitted for recognized capability tokens;
old clients/unknown tokens retain legacy behavior. Org/personal skill namesakes
remain. Do not remove global allowlist entries required by older clients or
delete/change seed bodies merely to unshare. Test bundle matrix and files.
Fix hosted show_skills/nudge discovery to honor UI/both surface consistently
with native skill sync. Do not refactor pod delivery or seed lifecycle.

## Task 4: Review, verification, IDE handoff

Independent spec/code review of lifecycle and export, behavioral skill reader
tests, link/helper checks, CLI race suite and targeted AF suite. Report exact
remaining limits. Open the nested skill folder and SKILL.md in the installed
IDE. Do not run live login/refresh or retire local skills during validation;
local skill installation requires careful inventory and separate tested action.

## Progress

- [x] Approved architecture and worktrees; bases verified.
- [x] Baseline behavior and automated tests captured.
- [x] Task 1 lifecycle and routing.
- [x] Task 2 content, helpers and source map.
- [x] Task 3 export and hosted discoverability.
- [x] Task 4 review, verification and IDE handoff.

## Design decisions

External worktrees avoid editing/committing ignored-directory policy in the
user's existing checkouts. Capability unsharing is intentionally rolling-upgrade
compatible: removing legacy delivery globally before replacement installation
would leave older clients without capabilities. Local installed roots are not
modified just to demonstrate code; tests use isolated homes.
