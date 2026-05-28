# praxis-onboarding — KT / Handover

**From:** Anshul · **To:** Samarthya · **Date:** 2026-05-27
**Branch:** `feat/praxis-onboarding-embedded-skill`
**Design spec:** `docs/superpowers/specs/2026-05-27-praxis-onboarding-flows-design.md`

---

## TL;DR

A new **binary-embedded multi-file skill** called `praxis-onboarding` that
runs guided onboarding journeys against the user's Facets CP. Engine + flow
registry; the first flow (`first-deployment`) walks a user from empty CP →
modules → tweak → project → env → real deploy → teardown. Plumbing in
`internal/skillinstall` was extended to support multi-file embedded skills
(`InstallTree` over a `go:embed` tree). Locally installed and behaviorally
tested against the live MCP gateway — needs validation on a **vanilla
(brand-new) control plane** which I don't have access to.

---

## 1. What problem this solves

A freshly Praxis-connected control plane ships **empty** — no catalog, no
projects, nothing. A new Praxis adopter has no clear path from "I logged in"
to "I get Facets." `praxis-learning` is a *reactive* topic teacher; this is
the *guided journey* counterpart. It hands off to existing skills
(`praxis-facets-blueprint`, `praxis-build-facets-module`, `praxis-learning`,
`praxis-release-debugging`) so it doesn't duplicate authoritative content —
it orchestrates them.

Design intent: **engine + flow registry**. The first flow is the simple
"first-deployment" sample E2E; more flows can be added later as separate
files under `flows/` without touching the engine.

---

## 2. What's been done

### Skill content (the markdown)

`internal/skillinstall/embedded/praxis-onboarding/` (the single source of
truth — embedded into the binary):
- `SKILL.md` — the engine: dispatch, the teach→act→check→record loop, the
  **two-gateway execution model** (MCP = gateway to Praxis; `raptor` via
  `run_raptor_cli` = gateway to Facets CP), **tiered Safety** (HARD GATE for
  billable/destructive/sandbox; SOFT for free/reversible), progress-file
  format, flow registry, handoffs, and a red-flags rationalization table.
- `flows/first-deployment.md` — the 9-stop journey, with real raptor verbs
  taken from `raptor --help`, conditional behavior on the starting state
  (skip import if catalog already populated, etc.), and explicit handoffs.

### Go plumbing (`internal/skillinstall/`)

Additive — does not break the existing single-file install path:
- **`embedded.go`** (new) — `//go:embed embedded/praxis-onboarding` + a
  `treeSkills()` registry + `isTreeSkill` / `treeSkillFS` helpers.
- **`installer.go`** — `Install(name)` now dispatches tree skills to a new
  **`InstallTree`** (walks an `fs.FS`, recreates the layout under the host's
  skill dir, records one canonical receipt entry per host pointing at the
  tree's `SKILL.md`). `Uninstall` does `RemoveAll` for tree skills. `Refresh`
  rewrites the tree from embed on tree-skill receipt entries.
- **`dummy.go`** — `IsMetaSkill` / `MetaSkillNames` now include tree skills,
  so the existing `praxis login` / `refresh-skills` install loop iterates
  them and `UninstallByPrefix("praxis-")` preserves them on profile switches.
- **`embedded_test.go`** (new) — TDD coverage: meta-skill names include
  tree, `isTreeSkill`, `Install` writes whole tree + correct receipt,
  `Uninstall` removes whole tree, `UninstallByPrefix` preserves it,
  `Refresh` rewrites the tree.

`make test` (= `go test -race ./...`) is green. `internal/skillinstall`
coverage **77.3%** (≥75% policy). `gofmt` + `go vet` clean.

### Iterations done so far

1. RED/GREEN/REFACTOR on the *skill content* (writing-skills methodology) —
   subagent baselines exposed 6 gaps; the skill closed them, including a
   confirm-under-pressure loophole that I REFACTORed into a **tiered**
   confirmation rule (HARD GATE vs SOFT).
2. First live run by Anshul exposed several content errors which are now
   fixed in this branch:
   - **Two-gateway model made explicit** (MCP vs raptor). My earlier dotted
     `raptor_cli.run_raptor_cli` shorthand was misleading and is gone — every
     call is the literal `praxis mcp <server> <fn>` form.
   - **Real raptor verbs** replacing my guessed syntax — `get projects`,
     `apply resource T/F/V`, `plan`, `create release -w`, `get releases`,
     `get accounts`, `create account`, `destroy` (the last to be discovered
     via `--help` rather than hardcoded). Verified live:
     `praxis mcp raptor_cli run_raptor_cli --arg command='get projects'`
     returns the project list under `credential_source: user_pat`.
   - **Conditional import** — Stop 2 skips when `catalog_ops
     get_existing_catalog` is non-empty; only imports on a vanilla CP.
   - **Cloud layer corrected** — clouds available to deploy come from
     `raptor get accounts` (Facets CP), NOT `cloud_cli list_cloud_integrations`
     (Praxis layer, a different thing).

### Parked

- **Guided "link Facets CP" step.** No first-class command/MCP exposes
  Facets-integration linking — only `praxis login --url <cp>` establishes the
  PAT. Stop 0 currently treats a linked CP as a **prerequisite** and tells
  the user to run `praxis login` and resume. Tracked in flow as task
  `link-facets-cp-capability` (under `agent-factory-enhancements`, medium
  priority).

---

## 3. What still needs testing — on a vanilla CP

I've only tested on my own (well-set-up) CP. The interesting cases live on a
**brand-new control plane**:

| # | Scenario | Expected behavior |
|---|---|---|
| T1 | Praxis logged in, CP fully empty (no catalog, no clouds) | Stop 0 detects everything empty; sandbox confirmed; Stop 2 imports `facets/<cloud>`; Stop 4 prep prompts cloud linking via `raptor create account` (HARD GATE) |
| T2 | CP has catalog already, no clouds | Stop 2 **skips import** with a short summary; Stop 4 prep links cloud |
| T3 | CP has catalog + at least one cloud account | Stop 2 skips; Stop 4 picks up the existing cloud account and proceeds straight to resource/env/release |
| T4 | No Facets integration linked at all (`list_facets_integrations` empty) | Stop 0 stops and tells user to run `praxis login`. No scripted linking. |
| T5 | Pressure path — user says "just do it, don't ask" | Engine still HARD-GATES the sandbox check, the release (Stop 6 — billable), and the destroy (Stop 8). SOFT steps announce-and-proceed. |
| T6 | Full end-to-end on a real cloud (tiny resource) | Releases succeeds, `Stop 7` shows outputs, Stop 8 destroy completes cleanly. |
| T7 | Teardown completeness | After Stop 8, no orphaned project/env on the CP; imported catalog is kept (free) by default. |

What I **could not** test locally: T1, T2, T4, T6, T7 in earnest. T3 is what
my CP looks like.

---

## 4. How to test it

```bash
# 1. Fetch the branch
git fetch origin feat/praxis-onboarding-embedded-skill
git checkout feat/praxis-onboarding-embedded-skill

# 2. Build the dev binary
make build         # produces ./praxis with the embedded skill inside

# 3. Install the skill into your local AI hosts.
#    refresh-skills is the no-browser path — requires you to already be
#    `praxis login`'d (`praxis status` should show logged_in: true).
./praxis refresh-skills

# 4. Verify the skill landed
ls ~/.claude/skills/praxis-onboarding/
#   SKILL.md   flows/first-deployment.md

# 5. Open a FRESH Claude Code session against the test CP and type one of:
#       "onboard me to Facets"
#       "get me started"
#       "set up my first deployment"
#    The praxis-onboarding skill should auto-trigger.
```

**For the vanilla-CP tests**, point Praxis at a brand-new CP first:
```bash
./praxis login --url https://<the-vanilla-cp>.console.facets.cloud
./praxis refresh-skills      # reinstall meta-skills under the new profile
```
Then start a fresh Claude session and run the flow.

**During the run, watch for:**
- Every mutating raptor command is preceded by the literal command string the
  agent will run, before any confirm. If the agent improvises a raptor verb
  not in `raptor --help`, flag it — the skill says discover, don't invent.
- The release (Stop 6) gets a *separate explicit yes* with the cloud-cost
  language. Try saying "don't ask me, just deploy" earlier in the session and
  verify the gate still holds.
- After Stop 8 destroy, the CP should be back to where it started (catalog
  kept by default unless you opted out).

**Failures worth filing back to me:**
- Wrong raptor command syntax / a verb that doesn't exist on the real CP.
- Stop 0 misreading state (e.g. claims no cloud when `raptor get accounts`
  shows one).
- Any Safety gate erodes under pressure.
- Anything about the **vanilla** experience that doesn't match the table in §3.

---

## 5. File map

```
internal/skillinstall/
├── embedded/praxis-onboarding/
│   ├── SKILL.md                    ← engine (two gateways, tiered safety,
│   │                                  flow registry, handoffs)
│   └── flows/
│       └── first-deployment.md     ← the 9 stops, real raptor verbs
├── embedded.go                     ← go:embed + treeSkills registry
├── embedded_test.go                ← tree-skill TDD
├── installer.go                    ← Install dispatch, InstallTree,
│                                     Uninstall/Refresh tree handling
└── dummy.go                        ← IsMetaSkill/MetaSkillNames include tree

docs/superpowers/specs/
├── 2026-05-27-praxis-onboarding-flows-design.md   ← design spec
└── 2026-05-27-praxis-onboarding-handover.md       ← this file
```

Authoritative skill content = `internal/skillinstall/embedded/...`. Anything
under `/cmd/praxis-*/` is gitignored test-pollution from old test leaks; do
not edit there.

---

## 6. Conventions to keep if you extend it

- **Single source of truth** for the skill content is the embedded dir, not
  any catalog server. The skill is binary-embedded by design (per Anshul) so
  multi-file works.
- **Adding a new flow** = drop `flows/<slug>.md` next to first-deployment +
  add a row in the SKILL.md flow registry. The engine, safety tiers, and
  progress format are reused unchanged.
- **TDD non-negotiable** per `CLAUDE.md` in this repo: every new
  `skillinstall` function lands with a `*_test.go` in the same commit;
  ≥75% coverage; `make test` green.
- **No `{{BRAND_NAME}}` placeholder** in embedded content — meta-skills are
  not server-templated (the existing dummy skills hardcode "Facets"/"Praxis"
  the same way).

---

## 7. Related context

- Design: `docs/superpowers/specs/2026-05-27-praxis-onboarding-flows-design.md`
- Flow tasks (Anshul's tracker):
  - `onboarding-flows` — this task, has the iteration history.
  - `link-facets-cp-capability` — the parked "guided Facets CP linking"
    follow-up.

Ping me on Slack if anything is unclear, especially around the two-gateway
model — that was the source of the biggest bug in the first live run.
