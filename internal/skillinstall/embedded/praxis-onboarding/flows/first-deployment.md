# Flow: first-deployment

**id:** `first-deployment`
**Goal:** Take a user through one complete, real loop — make sure their
control plane is linked and has modules, tweak a module, build a project,
deploy a minimal sample to their cloud, verify it, then tear it down. By the
end they understand the Facets mental model because they *did* it.

Run each stop with the engine loop in SKILL.md (teach → act → check → record).
Honor the SKILL.md Safety tiers. All raptor steps run through
`praxis mcp raptor_cli run_raptor_cli --arg command='<the raptor command>'`.
Labels:
- `[RO]` — read-only, runs freely.
- `[SOFT]` — free/reversible mutation; confirm normally, or announce-and-proceed
  if the user opted into autonomy.
- `[HARD GATE]` — billable, destructive, the sandbox check, or anything needing
  credentials; **explicit yes always, even under "don't ask me".**

> **Adapt to the starting state.** The CP may be vanilla (nothing linked) or
> already set up. Stop 0 detects which, and later stops *skip* work that's
> already done (e.g. modules already in the catalog). Never re-import or
> re-link something that already exists.

---

## Stop 0 — Connect & orient  `[RO]` (+ `[HARD GATE]` sandbox check)

**Teach:** Two gateways: MCP tools reach *Praxis*; `raptor` (via
`run_raptor_cli`) reaches your *Facets control plane*. First, figure out what's
already wired up — don't assume.

Run these and read the results:

```
praxis mcp raptor_cli list_facets_integrations    # is a Facets CP linked? (PAT)
praxis mcp catalog_ops get_existing_catalog        # is the module catalog populated?
raptor get accounts                                # which cloud accounts are linked IN Facets
```

Branch on what you find:

- **No Facets CP linked** (`list_facets_integrations` returns no integration /
  no active PAT): a linked CP is a **prerequisite** for this flow. Guided
  linking is not built yet (parked for a future capability). For now, tell the
  user to run `praxis login` against their CP so the PAT is established, then
  resume onboarding. Do not try to script the link.
- **Catalog already populated:** good — you will SKIP the import in Stop 2.
- **Catalog empty:** you will import in Stop 2.
- **No cloud account in `get accounts`:** you'll link one before deploying
  (Stop 4 prep). Note: do NOT use `cloud_cli list_cloud_integrations` to judge
  this — that's the Praxis layer, not the Facets CP.

**Then, [HARD GATE]:** ask the user to confirm this CP is a throwaway/sandbox
they may freely create and destroy in, before any mutation.

---

## Stop 1 — The mental model  `[RO, no command]`

**Teach** (with this ASCII — mandatory). This mirrors raptor's own hierarchy:

```
PROJECT TYPE (e.g. "aws")           ── defines which RESOURCE TYPES are available
   └── maps to IaC MODULES (the Catalog)
PROJECT / Blueprint (e.g. "my-app")
   ├── RESOURCES        ── each uses a RESOURCE TYPE = type/flavor/version
   │                       (e.g. service/k8s/0.2), backed by an IaC module
   └── ENVIRONMENTS (dev, prod)
          └── RELEASE   ── terraform apply of the blueprint to this environment
```

Catalog holds modules → a Project composes them into Resources → a Release
deploys them to an Environment.

---

## Stop 2 — Modules in the catalog  `[SOFT]` (conditional)

**Teach:** Resources are backed by IaC modules in the catalog. Facets ships
official, per-cloud *project types* — a curated module bundle — imported in one
command.

- **If Stop 0 found the catalog already populated:** SKIP the import. Show a
  short summary of `get_existing_catalog` and move on. Do not re-import.
- **If the catalog is empty:** import the bundle for the linked cloud
  (`<cloud>` ∈ `aws` | `gcp` | `azure`, taken from `get accounts`):

  ```
  raptor import project-type --managed facets/<cloud>
  ```
  Then re-run `praxis mcp catalog_ops get_existing_catalog` and show the result.

> To author a module from scratch instead, hand off to
> `praxis-build-facets-module` — but for onboarding the managed import is the path.

---

## Stop 3 — Tweak a module  `[SOFT]`

**Teach:** Modules are **editable contracts**, not black boxes — each exposes a
spec (inputs/outputs) you can inspect and change.

1. List/inspect: `raptor get resource-types`, then
   `raptor describe module <type>/<flavor>/<version>` for one the user picks
   ("pick whatever looks interesting").
2. Hand off to `praxis-build-facets-module` to change one small spec field and
   re-publish the module.
3. Show the before/after. Keep it to one small, reversible edit.

---

## Stop 4 — Create a project & add a resource  `[SOFT]` (cloud link is `[HARD GATE]`)

**Teach:** A *project* (blueprint) declares the resources you want, composed
from catalog modules. Use a clearly disposable name (suggest `praxis-hello`).

Hand off to `praxis-facets-blueprint` for the authoritative commands. Outline:

- **Ensure a cloud account exists.** If `raptor get accounts` showed none,
  link one now — **[HARD GATE]**, needs cloud credentials:
  ```
  raptor create account --provider <aws|gcp|azure> --name <name> -w
  ```
- Create the project (via the blueprint skill).
- Add **one small, cheap resource** from the catalog, wiring the cloud account:
  ```
  raptor get resource-types
  raptor apply resource <type>/<flavor>/<version> -p praxis-hello -n hello \
    --input cloud_account=cloud_account/<account-name> --set <minimal fields> --dry-run
  ```
  Confirm the project name first. Nothing is deployed yet — this is declared intent.

---

## Stop 5 — Add an environment  `[SOFT]`

**Teach:** An *environment* is a concrete deployment target within the project.

```
raptor get environments -p praxis-hello      # see what's there
raptor create environment -p praxis-hello    # discover exact flags with: raptor create environment --help
```

Hand off to `praxis-facets-blueprint`. Still nothing provisioned — the release
is the action.

---

## Stop 6 — Release & deploy  `[HARD GATE — billable]`

**Teach:** A *release* runs terraform to apply the blueprint to the environment —
this **provisions real cloud infrastructure**. Plan first, show the diff, then
deploy.

```
raptor plan -p praxis-hello -e <env>                 # validate + preview
```

Walk the plan through with the user. **Before applying, state plainly that this
creates billable cloud resources and roughly what.** Get a separate, explicit
yes (required even under "don't ask me"). Then:

```
raptor create release -p praxis-hello -e <env> -w    # deploy (waits for completion)
```

On failure, hand off to `praxis-release-debugging` / `troubleshoot` and read the
logs together: `raptor logs release -p praxis-hello -e <env> <release-id>`.

---

## Stop 7 — Verify it's live  `[RO]`

**Teach:** Close the loop with read-only evidence — the thing you declared now
exists in a real backend.

```
raptor get releases -p praxis-hello -e <env>                 # deployment history
raptor get resources -p praxis-hello                          # the blueprint
raptor get outputs -p praxis-hello -e <env> <type>/<name>     # runtime values
```

If it landed in k8s: `praxis mcp k8s_cli run_k8s_cli --arg command='get pods -A'`.
Then recap in 4 lines how module / catalog / project / environment / release relate.

---

## Stop 8 — Teardown  `[HARD GATE — destructive]`

**Teach:** Cleanup is part of the lesson — don't leave a sample quietly billing.
**Always offer this**, even if the user is in a hurry.

Discover the exact destroy syntax first (don't guess):

```
raptor destroy --help
```

Then destroy the sample's resources/environment for the project (on explicit
yes, after showing what will be destroyed). Default to **keeping** the imported
catalog (it's free) for future practice. Do a final read-only check
(`raptor get resources -p praxis-hello`, `raptor get environments -p praxis-hello`)
that no orphans remain. Mark the flow complete in the progress file.

---

## Done when

- The user watched one full loop — ensure links → modules → tweak → project →
  env → deploy → verify → teardown — end to end.
- They can name the core objects (project type, module/catalog, project/blueprint,
  resource, environment, release) and how they relate.
- Nothing live remains unless the user explicitly chose to keep it.

Offer a natural next step: author a module from scratch, or (later) the
Praxis-itself onboarding flow.
