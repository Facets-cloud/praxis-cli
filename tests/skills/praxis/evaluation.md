# Consolidated Praxis v1 evaluation

Source bases: praxis-cli 5663237; Agent Factory de9d8850 (2026-09-10).
Sources live in internal/skillinstall/embedded/praxis; this evaluation is not
shipped as part of the operational skill. Scenarios are in scenarios.json.

## Follow-up: CLI-only export stop (2026-09-10)

The user superseded opt-in server unsharing: all 30 replaced GLOBALs now stop
exporting to every CLI, without changing seeds, authored surfaces, web/pod
delivery or scoped namesakes. The older compatibility results below describe
the initial implementation, not the updated server policy.

TDD: seven expected failures demonstrated legacy GLOBALs still exporting;
after the policy change all nine focused cases passed. The real-store pipeline
test retains Praxis/Raptor GLOBAL rows and web skill bodies while exporting
only scoped namesakes. The related AF suite now passes 169 tests (eight redundant
per-name export tests replaced by the all-30 regression); the existing
Starlette/httpx deprecation warning remains. Ten offline package/helper tests,
skill validation, focused CLI installer/catalog/command tests, Ruff and diff
checks pass. Lifecycle guidance now separates unconditional server policy from
local replacement verification. No live refresh, deployment or commit performed.

## Follow-up: Raptor dependency install and upgrade (2026-09-10)

Behavior-first regressions caught missing Raptor skill installation, missing
binary installation at login, omitted Raptor upgrade when Praxis was current,
and refresh JSON falsely reporting completeness after a Raptor failure.
The passing tests exercise checksum/size validation, no-clobber activation,
preserved binaries/symlinks, shared/project host roots, real export subprocesses,
old/incomplete bundle rejection, independent auth/snapshot continuation, and
separate JSON results after successful Praxis update or Homebrew deferral.

Built real Raptor/Praxis candidates into `/tmp/praxis-raptor-smoke.6cmEPm`.
Praxis setup in a disposable home installed both packages for Claude/Codex;
Raptor trees at both targets and the Praxis tree matched their source directories
byte-for-byte. A second setup preserved the existing Raptor packages. This used
the local Raptor checkout containing the uncommitted consolidated skill, not a
published release. No real user home, credentials or live binary were upgraded.

## RED: installed legacy reader

Independent reader Averroes (01a08782-79d4-7f61-92be-2fc0a3867ca1) read installed
skills, with no live operations/credentials. Seven prompts preceded new prose.

- shared-identity: failed. Verbatim: “Switching Praxis to staging does not switch
  Raptor.” Reported `-p > PRAXIS_PROFILE > project .praxis pointer > global pointer`
  and global separate stores. Current source uses shared Facets PATs/defaults.
- mcp-output: partial. Correct output distinction, but “no combined-use or
  precedence rule” for --body/--arg; source says body replaces arguments.
- duty-trigger: partial. Correct MCP trigger and paused behavior; native `duty run`
  semantics were “not established by the read instructions”.
- blueprint-import-boundary: mixed. Chose current Raptor routes but also loaded
  praxis-zero-change-import and encountered conflicting spec-prefix doctrine.
- catalog-read-build: conflicting. Current reader needed no sync, older builder
  still prescribed local sync for ordinary readers.
- cache-cutover: exposed a real source limitation: monitor checks types/sizes,
  not values/TTL/lag, and can pass an empty sample. Prose overclaimed fidelity.
- gateway-effects: partial. New Relic/account-sync effects recognized; SSM
  contract unknown from the old instructions, while current source allows send-command.

The new references address those demonstrated retrieval/interpretation failures.
They preserve legacy helpers with explicit limits rather than claiming those
helpers have been upgraded into complete migration validators.

## GREEN / review

Independent candidate reader Dewey (01a08797-6050-7052-8f8c-4efa3a55d429) received
the same seven prompts with access only to the new root, routed references and
canonical Raptor. It was forbidden from reading this ledger/scenario answers.
All seven retrieved the intended boundaries: shared PAT/default selection,
body replacement/output variants, native run vs paused MCP trigger, Git/override/
zero-change import separation, catalog read vs publish-root workflow, incomplete
cache evidence, and gateway mutation exceptions. It used relevant references,
not the whole package or retired prefixed recipes.

One genuine gap: same-name PAT/API-key collisions. Verified against
internal/credentials/credentials.go:loadWith and added the Facets-section-wins
rule to context-and-access. Installed help/deployed contracts were not exercised
by this read-only test.

Independent reader Kepler (01a0879c-399d-7fa1-9c3a-c460dc52d830) evaluated the six
additional scenarios without reading evaluation answers. DAG lease/completion
reconciliation, manual gates, exact artifact identity, UI transport/registration,
secret read-back limits, retirement provenance and incomplete waste coverage all
retrieved the intended decisions. It found two preserved-helper inconsistencies:
cp.js caches non-2xx fallback identity and omits metadata timeout; secret describe
errors conflate denial with absence and its header omits read permissions.
References now explicitly identify these defects and prohibit relying on those
claims. Helpers remain source-preserved, not silently upgraded/certified.

Offline tests: 10 package/helper characterization tests passed. They execute
both draft generators with example specs and an empty PATH, check cache command
guards/env auth and known empty/TTL blind spots, secret classification/stdin,
catalog errors/artifact scope, all relative links, syntax/assets and one-entrypoint
lazy routing. Skill-creator quick_validate passed. None contacts live systems.
Template smoke: npm install --ignore-scripts and npm run build passed in isolated
/tmp/praxis-component-smoke.TOryi7, yielding dist/my-web-component.js (613.35KB;
195.05KB gzip). This validates bundling, not authenticated rendering, reconnect
behavior, transport correctness or provider contracts. No npm artifacts were
added to the skill package.

Installer/code verification: go test -race -cover ./... passes all CLI packages;
cmd 73.8%, skillinstall 80.4%, skillcatalog 87.5%, ighook 96.4%. Agent Factory's
177 related tests pass, including old/new bundle compatibility, scoped namesakes
and unchanged pod delivery. One existing Starlette/httpx deprecation warning.
Ruff/format and git diff --check pass. Code review outcome recorded separately
in the implementation ledger; no live login/refresh or real-host retirement run.
Do not infer semantic correctness from a frontmatter/link validator alone.

## Raptor installation and upgrade follow-up

Added failing behavior tests before implementing missing-binary bootstrap,
canonical whole-tree export/installation, login partial-success reporting and
internal `raptor upgrade` delegation (including already-current Praxis and
Homebrew deferral). Download tests use fake HTTP responses; upgrade tests run
disposable executables. These do not exercise live authentication or releases.

Independent code review found three additional regressions, each reproduced
before its fix: export under nested TMPDIR could discover parent credentials;
new-only package creation could overwrite a concurrent independent install;
shared Codex/Gemini installation could duplicate an existing native package.
The fixes fence credential lookup with an empty staging credentials file, carry
new-only ownership through staging and OS-exclusive activation, and select a
same-scope native destination when shared discovery would duplicate a peer.
Project and single-host-selection cases are covered.

Fresh verification after those fixes: `go test -race -cover ./...` passes
(cmd 74.4%, skillinstall 81.4%, raptorinstall 84.8%); `go vet ./...` and all 10
offline skill tests pass. Actual local Raptor/Praxis binaries were also exercised
using disposable homes: both canonical trees installed, repeat setup preserved
Raptor, and exported trees matched their repository sources. This is a local
source-bundle check, not proof that the current public Raptor release embeds the
consolidated skill. No live home, profile, installed binary or server was changed.
Follow-up independent review cleared all three findings and independently
verified the focused race regressions and Linux cross-compilation.

## Pre-PR live distribution checks

Subsequent authorized tests exercised the actual local installation, after
backing up its binaries and skill state. Real setup succeeded for all detected
hosts and repeat setup preserved Raptor packages/source symlinks. Full installed
trees matched source, and credential hashes were unchanged.

Copies of the development executables, isolated from the real home/PATH, ran
`praxis update --json` against real GitHub releases: Praxis became 1.11.0 and the
nested `raptor upgrade` completed at 0.1.103. Direct Raptor skill installation and
version-triggered skill re-extraction also passed in a disposable home.

A fresh missing-Raptor bootstrap downloaded and installed public Raptor 0.1.103,
but its consolidated skill export failed because that release lacks the package.
The CLI correctly returned incomplete setup. Publishing the Raptor bundle is a
rollout prerequisite; the passing local-source smoke is not release readiness.
The existing development-version freshness parser also misclassifies
`0.development` as absent; this unrelated status-display issue remains open.

Pre-PR verification: the complete Agent Factory backend suite passed 5,757 tests
after updating the DAG export test and preserving release-debugging source-scan
coverage with the portability inventory. Mypy passed across 972 source files;
changed Python files passed Ruff. The suite emitted 34 dependency/mock warnings.
Praxis's complete race/coverage suite, vet and 10 offline skill tests passed again.
