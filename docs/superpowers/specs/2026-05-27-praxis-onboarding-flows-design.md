# praxis-onboarding — guided onboarding flows for Facets & Praxis

**Date:** 2026-05-27
**Status:** Design — pending review
**Repo:** `praxis-cli`

## What

A new **binary-embedded, multi-file skill** (`praxis-onboarding`) that runs
*guided, hands-on onboarding journeys* turning a brand-new Praxis adopter from
"empty control plane" into "I deployed something real and understand Facets."

It is built as an **engine + a registry of flows**, not a single hardcoded
journey. The first flow shipped is a **sample end-to-end deployment** (import
modules → tweak a module → create project/env → release → teardown). More
flows are added later as additional files — the engine is reused unchanged.

## Why

- A freshly Praxis-connected **control plane ships empty** — no catalog, no
  projects. A read-only "tour of your infra" is meaningless because there's
  nothing to look at. The only way to teach Facets to a new adopter is to
  **help them make the CP work**: import modules, tweak one, and deploy
  something real to cloud.
- The audience is **Praxis CLI adopters**: people who just ran `praxis login`,
  received the skill catalog, and need Facets fluency to actually use the
  CLI's infra capabilities.
- `praxis-learning` already exists but is a *reactive* topic teacher
  ("learn about X" → chapter + quiz). Onboarding is the complementary
  *guided path*: a journey with a defined start and end. Onboarding hands off
  to `praxis-learning` for any deep-dive.
- There will be **many onboarding flows** over time (different clouds, roles,
  scenarios). This design must make adding a flow cheap: drop in one file.

## Where

work_dir: `/Users/anshulsao/Facets/Code/Facets/praxis-cli`

## Audience & scope decisions (locked during brainstorming)

| Decision | Choice |
|---|---|
| Primary audience | Praxis CLI adopters (post-`praxis login`, empty CP) |
| Relationship to `praxis-learning` | New dedicated skill; hands off to learning for deep dives |
| Experience | Hands-on guided *doing* (mutating, ends in a real cloud deploy) |
| First flow's deploy target | Minimal sample resource + explicit teardown (no lingering cost) |
| Module source | `raptor import project-type --managed facets/<cloud>` populates catalog; user then tweaks a module **of their own choosing** |
| Progress persistence | Local file `~/.praxis/onboarding-progress.json` (cross-session resume) |
| Packaging | **Embedded in the CLI binary** (NOT org catalog) → enables a **multi-file skill** |

## Architecture

### Packaging: embedded multi-file skill

Unlike org-catalog skills (single `SKILL.md` body fetched from the server),
`praxis-onboarding` is **embedded in the binary** alongside the existing
meta-skills (`praxis`, `praxis-memory`). Embedding lets us ship a **directory
tree** of markdown instead of one file:

```
praxis-onboarding/
├── SKILL.md                      ← engine + dispatch menu + flow registry
└── flows/
    └── first-deployment.md       ← flow #1 (ships now)
    └── <future-flow>.md          ← added later, engine untouched
```

The host (Claude Code / Cursor / Gemini) supports skills whose `SKILL.md`
references sibling files; the engine tells the host to read the chosen flow
file on demand.

```
                 praxis login / refresh-skills
                            │
                            ▼
        ┌───────────────────────────────────────────┐
        │  internal/skillinstall                      │
        │  ┌─────────────────────────────────────┐   │
        │  │ embedded fs.FS (go:embed)            │   │
        │  │   praxis-onboarding/SKILL.md         │   │
        │  │   praxis-onboarding/flows/*.md       │   │
        │  └──────────────┬──────────────────────┘   │
        │       InstallTree(skillName, fsys, hosts)   │
        └──────────────────┬──────────────────────────┘
                           │ writes tree per host
        ┌──────────────────▼──────────────────────────┐
        │ ~/.claude/skills/praxis-onboarding/SKILL.md  │
        │ ~/.claude/skills/praxis-onboarding/flows/... │
        │ (+ ~/.agents, ~/.gemini)                     │
        └──────────────────────────────────────────────┘
```

### Go changes in `internal/skillinstall`

Today: `Install(name)` → `ContentFor(name)` (string from `dummy.go`) →
`InstallWithBody` writes a single `SKILL.md` (`installer.go:64-95`).

New, additive (does not change existing single-file paths):

1. **`InstallTree(skillName string, fsys fs.FS, hosts []harness.Harness)`** —
   walks `fsys`, recreating the directory tree under each host's
   `<SkillDir>/<skillName>/`. Records the installation in the receipt
   (canonical `Path` = the `SKILL.md` at the tree root, so `status`/uninstall
   keep working).
2. **Embedded FS** — a `go:embed` directory (e.g.
   `internal/skillinstall/embedded/praxis-onboarding/**`) exposed via a
   function like `OnboardingFS() fs.FS`. This is the multi-file analogue of
   `dummy.go`'s `ContentFor`.
3. **Wire into install path** — `praxis login` / `refresh-skills` install the
   embedded meta-skills; add `praxis-onboarding` to that set, routed through
   `InstallTree` instead of `Install`.
4. **Preservation** — `UninstallByPrefix` already preserves meta-skills
   (`installer.go:135+`). `praxis-onboarding` must be added to the preserved
   set so profile switches / `logout --all` semantics match the other
   embedded skills.
5. **Uninstall of a tree** — `Uninstall`/teardown must remove the whole skill
   dir (tree), not just `SKILL.md`.

All of the above land with table-driven tests using `t.TempDir()` and the
existing harness stubs (per `CLAUDE.md`: ≥75% coverage, tests in the same
commit).

### The engine (SKILL.md)

Shared prose, flow-agnostic. Responsibilities:

- **Dispatch.** On invocation ("onboard me", "get started", "resume
  onboarding"), read `~/.praxis/onboarding-progress.json`, list available
  flows from the registry, and let the user pick (or resume an in-progress
  one).
- **Run-a-stop loop.** For each stop: present a short concept + a **mandatory
  ASCII diagram** (matching `praxis-learning`'s house style) → run the stop's
  real command → "got it? (yes / explain more / skip)" gate before advancing.
- **MCP shellout convention.** Reuse the `praxis-learning` header verbatim:
  any `<mcp>.<fn>(args)` becomes `praxis mcp <mcp> <fn> --arg k=v`. Discover
  tools via `praxis mcp --json` / `~/.praxis/mcp-tools.json`.
- **Safety guardrails.** Read-only stops run freely. **Mutating stops confirm
  first**, name the action, and for the release stop name the expected cloud
  cost. `run_raptor_cli` is general-purpose, so the engine issues *only* the
  specific documented subcommand per stop — never improvised mutations.
- **Progress.** After each completed stop, append the step index to the
  progress file under the flow's id. Resume reads it back.
- **Handoff.** Where a sibling skill is the expert, the engine names it and
  defers: `praxis-build-facets-module` / `praxis-design-facets-module`
  (module tweak), `praxis-facets-blueprint` (project/env/release),
  `praxis-learning` (concept deep-dives).

### The flow registry

A section in `SKILL.md` listing each flow as `{id, title, one-liner, file}`.
Adding flow #2 = add one row here + drop a new file in `flows/`. No engine
edits.

## Flow #1 — `first-deployment` (sample end-to-end)

File: `flows/first-deployment.md`. Steps:

```
0  Connect & orient
     read-only:  list_facets_integrations + list_cloud_integrations
     → identify the connected cloud (aws|gcp|azure); confirm the CP is empty.

1  Mental model                                          [ASCII, no command]
     Org → Project(Blueprint) → Environment → Resource
     Modules live in the Catalog; a Blueprint composes them.

2  Import modules                                        [MUTATING — confirm]
     raptor import project-type --managed facets/<cloud>
     (run via: praxis mcp raptor_cli run_raptor_cli --arg command='import …')
     → the empty catalog is now populated with a cloud's module bundle.

3  Tweak a module                                        [MUTATING — confirm]
     User picks ANY imported module "as they wish"; edit one spec field;
     re-register/update. Hand off to praxis-build-facets-module for depth.
     → teaches that modules are editable contracts, not black boxes.

4  Create a project (blueprint)                          [MUTATING — confirm]
     Scaffold a project from the imported modules.
     Hand off to praxis-facets-blueprint.

5  Add an environment                                    [MUTATING — confirm]
     Create a dev environment; wire the cloud integration.

6  Release & deploy                            [MUTATING — confirm + COST note]
     Run a release that provisions the minimal sample for real.
     Explicitly state expected cloud cost before proceeding.

7  Verify it's live                                              [read-only]
     Show release status / the running resource. Celebrate the loop.

8  Teardown                                              [MUTATING — confirm]
     Offer to destroy the sample so no cloud cost lingers.
     Mark the flow complete in the progress file.
```

Notes:
- Stops 2–8 each require explicit user confirmation before the mutating
  action; the engine surfaces the exact command it will run.
- Cloud is detected at Stop 0; the `facets/<cloud>` import arg follows from it.
- The "minimal sample" deployed at Stop 6 is the smallest cheap resource that
  proves the import→deploy loop end to end (exact resource TBD — see open
  questions).

## Out of scope (v1)

- Additional flows beyond `first-deployment` (the framework supports them; we
  ship one).
- A dedicated `praxis onboard` cobra command. Entry is via the skill trigger
  ("onboard me"). A command can be added later if a deterministic entry point
  is wanted.
- Scheduling / automated re-runs.
- Org-catalog distribution (we embed instead).
- A Praxis-the-CLI onboarding flow (a sibling flow for "understand Praxis
  itself" comes after Facets-first).

## Open questions

1. **Exact minimal sample** at Stop 6 — which single cheap resource best
   proves the loop on each cloud (e.g. a hello-world `Service`)? Confirm per
   cloud, or pick one cloud for v1.
2. **Multi-file host support nuance** — confirm Cursor and Gemini CLI hosts
   read sibling files referenced from `SKILL.md` the same way Claude Code
   does; if a host is single-file-only, the engine falls back to inlining the
   chosen flow.
3. **Module tweak mechanics** — confirm the exact raptor command sequence to
   edit + re-register a single module's spec field post-import (depends on the
   `facets-modules-redesign` import model).
4. **Teardown completeness** — does a single release-destroy fully remove the
   sample, or are there residual catalog/project artifacts the teardown step
   should also clean?

---
*Before implementing, read CLAUDE.md in the work_dir. Implementation is
TDD: failing test first for every new `skillinstall` function, ≥75% coverage,
`make test` green.*
