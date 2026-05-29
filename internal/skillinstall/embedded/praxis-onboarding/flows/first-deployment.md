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

> **Trust the sandbox confirmation.** Once Stop 0 has the user's HARD-GATE
> confirmation that this is a sandbox CP, do NOT keep probing the user's
> local environment — no "do you have AWS/GCP/Azure CLI configured?", no
> "is `aws sts get-caller-identity` / `gcloud auth list` / `az account show`
> working?", no "is this terminal authenticated?". Assume the user can run
> a printed bootstrap script wherever they have cloud auth. **Default
> recommendation: the matching cloud provider's browser shell** — AWS
> CloudShell, Google Cloud Shell, or Azure Cloud Shell. Open the provider's
> web console, find the shell icon, paste the one-liner — zero local setup.
> Local CLI, an instance with an attached identity (EC2 role / GCP VM
> service account / Azure managed identity), and CI runners all work
> equally; the skill stays out of those choices.

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

## Stop 4 — Create a project & add resources  `[SOFT]` (cloud link is `[HARD GATE]`)

**Teach:** A *project* (blueprint) declares the resources you want, composed
from catalog modules. Four things have to happen in this stop, in order:

1. Link a cloud account at the CP/org level (HARD GATE — first cloud action).
2. Create the project — and **capture the canonical name** raptor returns
   (the CP may rewrite it; see 4b).
3. Add the project-level `cloud_account` blueprint resource (the second
   layer of the cloud_account dance).
4. Add your actual user resource (e.g. an S3 bucket).

Hand off to `praxis-facets-blueprint` for deeper blueprint patterns.

### 4a — Link a cloud account at the CP/org level  `[HARD GATE]`

If `raptor get accounts` showed none, link one now — this is the first
cloud-touching action.

```
raptor create account --provider <aws|gcp|azure|kubernetes> --name <account-name>
```

raptor registers a webhook and **prints a bootstrap script** (a one-liner)
the user then runs in any environment authenticated with the chosen cloud.
**DO NOT probe the user's local setup** — Stop 0 already confirmed
sandbox; trust that.

**Recommend the matching cloud provider's browser shell** as the default
zero-setup path. Tell the user, in plain language: "open your <provider>
web console, find the shell icon (AWS CloudShell / Google Cloud Shell /
Azure Cloud Shell), paste this one-liner". The script uses whichever cloud
account that shell session is signed into. Local CLI, instance-with-role,
and CI runners all work equally; don't push any one of them.

**Do not use `-w` via the MCP wrapper** — linking polls a webhook for
several minutes, exceeding the wrapper's 2-min HTTP timeout. Trigger
without `-w`, then poll:

```
until acct=$(raptor get accounts -o json 2>&1) && \
      [[ "$acct" =~ "<account-name>" ]]; do sleep 10; done
```

### 4b — Create the project and capture its canonical name  `[SOFT]`

**Important: the CP may rewrite the project name.** Facets has two customer
types, and they name projects differently:
- **SaaS:** the project name is suffixed with the CP UID (e.g. you type
  `praxis-hello`, the CP stores `praxis-hello-4788907041`).
- **PaaS:** the name stays as you typed it.

There is no flag to tell which type the CP is. So **always capture the
canonical name from the response** and use it in every subsequent raptor
command in this flow.

```
raptor create project praxis-hello --project-type <type> --clouds <cloud> -o json
```

Parse the `.name` field from the JSON response. Refer to it as `<project>`
in every later stop. **From this point on, never use the user-typed name in
raptor commands** — always use the captured `<project>`.

### 4c — Add the cloud_account blueprint resource  `[SOFT]`

Cloud accounts are **two-layered** in Facets — both layers are required:

```
Layer 1 (CP / org-wide)              Layer 2 (project blueprint)
─────────────────────────            ────────────────────────────
raptor create account ─────┐         raptor apply resource cloud_account/<provider>_provider/1.0
  --name <account-name>    │             -p <project> -n <account-name>    ← SAME NAME
                           └──── bound by name ────►
```

Layer 1 (Stop 4a) registers the account org-wide. Layer 2 (this step) makes
the account referenceable from THIS project's blueprint. **Without Layer 2**,
any resource that wires `--input cloud_account=cloud_account/<name>` errors
with `referenced resource ... not found in project`.

Add the cloud_account blueprint resource — the name MUST match Layer 1:

```
raptor apply resource cloud_account/<provider>_provider/1.0 \
  -p <project> -n <account-name>
```

Inputs are empty for this resource; it's a name-binding only. Runtime values
(CP-account-id, region) come from the env-level overrides in Stop 5b.

### 4d — Add your user resource(s)  `[SOFT]`

Now add **one small, cheap resource** from the catalog, wiring the
cloud_account blueprint resource you just declared. For onboarding,
`s3/standard/1.0` with `force_destroy=true` is a good choice (one small
bucket, easy teardown).

```
raptor get resource-types
raptor apply resource s3/standard/1.0 \
  -p <project> -n hello-bucket \
  --input cloud_account=cloud_account/<account-name> \
  --set force_destroy=true
```

Nothing is deployed yet — this is declared intent. Plan and release happen
at Stop 6.

---

## Stop 5 — Create, configure, and launch the environment  `[SOFT]` create+overrides · `[HARD GATE]` launch

**Teach:** An *environment* is a concrete deployment target within the project.
Creating it is just metadata. **Launching it** is the first cloud-touching
action — it bootstraps env-level infrastructure (state backend, runner role,
networking scaffolding) before any blueprint resource can be deployed.

Three substeps in order: create the env, apply any required env-level
overrides, then launch. Skipping the overrides causes launch to fail several
minutes in.

### 5a — Create the env  `[SOFT]`

```
raptor get environments -p <project>             # see what's there
raptor create environment <env> -p <project>     # discover exact flags with: raptor create environment --help
```

**Verify state.** A newly created env lands in **STOPPED** state — it is not
deployable yet:

```
raptor get environments -p <project> -o wide     # expect: state=STOPPED, cloud=NO_CLOUD
```

If state is anything other than STOPPED, stop and inspect before proceeding.

### 5b — Apply env-level overrides  `[SOFT]`

**Teach:** Some module spec fields are required but marked
`x-ui-overrides-only: true` in the module's `facets.yaml` — meaning they
**must** be set at the environment level, not in the blueprint. The
`raptor describe module` view **hides** these fields (known bug), so the
authoritative source is the module's `facets.yaml` itself.

**Skipping this for a resource whose module needs it will fail launch** several
minutes in with terraform errors like
`var.instance.spec is object with no attributes`. Check every resource in the
blueprint and apply the overrides it needs before Stop 5c.

**Known requirement in this flow** — the `cloud_account/aws_provider/1.0`
resource added at Stop 4 declares two REQUIRED override-only fields:
- `spec.cloud_account` — the CP-side account ID (from `raptor get accounts`)
- `spec.region` — the AWS region for this env

Discover the CP-side account ID, then apply the overrides:

```
raptor get accounts -o json                          # find the ID for your linked account
raptor apply override cloud_account/<account-name> -p <project> -e <env> \
  --set spec.cloud_account=<CP-account-id> \
  --set spec.region=<aws-region>                     # e.g. us-east-1
raptor get overrides -p <project> -e <env> cloud_account/<account-name>   # verify
```

**For any other resource in the blueprint**, check its module for required
override-only fields:

```
# download the module and inspect its facets.yaml
raptor get iac-module <kind>/<flavor>/<version> --save-to /tmp/<name>.zip
unzip -p /tmp/<name>.zip facets.yaml | grep -B1 -A2 x-ui-overrides-only
```

For each field listed in `required:` that ALSO has `x-ui-overrides-only: true`,
apply an override the same way. When `describe module` is fixed to surface
these fields, this manual discovery step goes away.

### 5c — Launch the env  `[HARD GATE — billable]`

**Launching is mandatory before any plan or release.** A plan against a STOPPED
env fails with `Cannot Release on a cluster in state: STOPPED`. There is no way
to skip this step — you MUST `raptor launch environment` before Stop 6.

State the cost plainly before launching. Env-bootstrap typically includes a
VPC, state backend, and runner IAM; on some project types it may also include
a NAT gateway (~$32/month) or similar billable infra. Get explicit yes
(required even under "don't ask me"). Then:

```
raptor launch environment <env> -p <project>
```

**Do not use `-w` via the MCP wrapper** — launch typically runs 1–5 min, which
exceeds the wrapper's 2-min HTTP timeout. Trigger without `-w`, then poll:

```
# poll until launch leaves IN_PROGRESS
until rel=$(raptor get releases -p <project> -e <env> 2>&1) && \
      ! [[ "$rel" =~ IN_PROGRESS ]]; do sleep 30; done
```

On success the env state flips to **RUNNING** and `raptor plan` (Stop 6)
becomes available. On failure (the most common cause is a missed override
from 5b), hand off to `praxis-release-debugging` and read the logs.

Hand off to `praxis-facets-blueprint` if the user wants deeper environment
patterns (overrides, multi-cloud env, etc.).

---

## Stop 6 — Tweak, plan, release  `[SOFT]` tweak · `[RO]` plan · `[HARD GATE]` release

**Teach:** With the environment now in RUNNING state (Stop 5c), a *release* runs
terraform to apply the blueprint to it — this **provisions real cloud
infrastructure** for your declared resources. The pattern is: make a small
deliberate change to the blueprint, plan to preview, **only apply if the plan
succeeded**.

### 6a — Make a small tweak to the s3 resource  `[SOFT]`

A change in the blueprint makes the plan output meaningful (and demonstrates
the iteration loop). Enable versioning on the s3 bucket — single boolean,
real-world useful, doesn't materially change cost on an empty bucket:

```
raptor apply resource s3/standard/1.0 -p <project> -n hello-bucket \
  --input cloud_account=cloud_account/<account-name> \
  --set force_destroy=true \
  --set versioning_enabled=true
```

(`apply resource` is idempotent — re-applying with new `--set` values updates
the existing resource declaration.)

### 6b — Plan  `[RO]`

```
raptor plan -p <project> -e <env>
```

**Verify the plan succeeded** before doing anything else:
- If plan returned a 504 / network timeout / terraform error → STOP. Hand off
  to `praxis-release-debugging` and read the logs. Do NOT proceed to apply.
- If plan succeeded → walk the diff through with the user. Plan should show the
  s3 bucket to be added with `versioning_enabled = true`.

### 6c — Release  `[HARD GATE — billable]`

**Only proceed if 6b's plan succeeded.** State plainly that this creates
billable cloud resources and roughly what (an empty S3 bucket ≈ $0/mo; if you
declared anything heavier, name it). Get a separate, explicit yes (required
even under "don't ask me"). Then:

```
raptor create release -p <project> -e <env>
```

**Do not use `-w` via the MCP wrapper** — release also exceeds the 2-min HTTP
timeout. Trigger without `-w`, then poll:

```
until rel=$(raptor get releases -p <project> -e <env> 2>&1) && \
      ! [[ "$rel" =~ IN_PROGRESS ]]; do sleep 30; done
```

On failure, hand off to `praxis-release-debugging` / `troubleshoot` and read the
logs together: `raptor logs release -p <project> -e <env> <release-id>`.

---

## Stop 7 — Verify it's live  `[RO]`

**Teach:** Close the loop with read-only evidence — the thing you declared now
exists in a real backend.

```
raptor get releases -p <project> -e <env>                 # deployment history
raptor get resources -p <project>                          # the blueprint
raptor get outputs -p <project> -e <env> <type>/<name>     # runtime values
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
(`raptor get resources -p <project>`, `raptor get environments -p <project>`)
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
