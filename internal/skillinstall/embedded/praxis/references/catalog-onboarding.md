# Build or enrich an ig catalog

This is the **builder** route. Ordinary reads use [Catalog reads](catalog-read.md)
without local sync/build tools. First list existing catalogs and inspect members:
infra-only is an intentional partial catalog, not full code/service coverage.
Offer enrichment if wanted; don't rebuild a usable catalog just to answer a query.

For building, check local `ig`, its backend dependencies (`ig doctor`), Praxis
authentication, and Raptor only when Facets inventory/infra build is needed.
Missing tools require an installation decision, not automatic setup. Use `ig`
as the interface; graphify is its backend, not an alternative command workflow.

## Map project, environments and repositories

1. Verify the Facets project and selected environments using the **raptor skill**.
   Pick from current evidence (cloud, state, recent release), not test-name guesses.
2. Discover artifact-bearing resources, then group by artifact/build identity so
   shared artifacts don't prompt for the same repo repeatedly. Raptor's artifact
   metadata and effective environment config are authoritative for mapping.
3. Find candidate repos through [Repository access](repository-access.md), an
   existing checkout or approved VCS access. Search the relevant org and personal
   repos when appropriate; paginate, not an arbitrary fixed first-N listing.
4. Verify repo identity and service mapping. Generic names are weak evidence;
   even a project-prefixed match is a candidate, not proof. If no plausible match
   exists, mark it unresolved/skipped and report it instead of forcing a choice.
5. Capture local `path` when present and canonical `git` URL. Path drives the local
   build; Git supplies portable identity. For monorepos record the actual member
   subdirectory. Verify it exists; don't invent a laptop path. One repo entry's
   primary `service` should be deliberate when several resources share a build.

[facets.py](../scripts/catalog/facets.py) preserves the old projects/envs/services
reader. It suppresses Raptor's update banner and reports skipped resource types,
but scans only service/application and known `spec.release.image/build.name`
shapes at blueprint level. It does not read all module-declared artifact fields,
resolve templates or environment overrides. Its empty output cannot establish
“no artifacts”; use Raptor's current contracts for completeness.

## Manifest and build

Choose a stable user-approved output path and preserve any existing manifest.
A minimal shape (verify against installed ig's schema):

```yaml
name: project-catalog
facets:
  project: canonical-project
  envs: [dev]
repos:
  - name: api
    path: /verified/checkout
    git: https://github.com/ORG/api.git
    service: api
```

Missing `path` allows ig's Git-based checkout when supported. Missing Git identity
makes local-only members nonportable; don't publish them as portable. Show the
mapping and skipped members before the build; don't require a host-specific modal
tool when a short direct question works.

Use `ig register MANIFEST`, then `ig build -p CATALOG -routes` for route-aware
connections. Optional enrichment adds model cost/data exposure; don't enable it
silently. Plain structural builds are valid if explicitly chosen, but lack HTTP
route extraction. Confirm flags with installed help. Propose `.graphifyignore`
only for real generated/vendor/fixture noise; don't exclude useful code by default.
Run `ig validate -p CATALOG` and inspect status/topology. Missing edges route to
[Extractors](catalog-extractors.md), not invented coupling.

## Publish (separate external write)

Remove builder-local paths from the portable manifest copy, retaining canonical
Git/member identity. Current Praxis requires the catalog flag:

```bash
praxis ig manifest push /absolute/portable.ig.yaml --catalog CATALOG
praxis ig publish /absolute/build-root --catalog CATALOG --member MEMBER
```

**Publish takes the build root**, containing
`member/MEMBER/graphify-out/graph.json`, not the member directory itself. For
multi-catalog output use the appropriate catalog's build root. It reads Git/SHA
from `member/MEMBER/member-meta.json` or explicit flags and uploads the graph;
do not claim every sidecar/local file is uploaded. Validate portability and source
SHA before publishing; do not include repos, credentials, caches or machine paths.

Verify published member/version and joined catalog. An accepted member publish
is not proof every intended member/edge exists. `praxis ig sync` remains available
for local bundles/build seeding; it is optional, not the default MCP reader path.
Use [Distributed CI](catalog-distributed-ci.md) for ongoing per-repo refresh.
