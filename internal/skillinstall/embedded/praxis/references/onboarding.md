# Guided onboarding

Use for an explicitly requested hands-on journey, not ordinary topic lookup.
Adapt to the user's current state and experience. Teach a short concept, show a
small ASCII relationship, perform the in-scope step, verify, then check whether
to continue or explain. Do not require a quiz or a new module publish to use Facets.

```text
Access -> existing capabilities -> sample scope -> configured intent
       -> reviewed deployment -> runtime evidence -> keep or clean up
```

1. **Orient:** [Context and access](context-and-access.md), selected console and
   available tools. Explain gateway integration vs Facets deployment account.
2. **Pick a journey:** cloud inspection, catalog reading, or first Facets
   deployment. Existing catalogs can be read without building; see
   [Catalog reads](catalog-read.md).
3. **First Facets deployment:** load the **raptor skill**, accounts/first-deployment
   route, and its configuration/lifecycle routes as needed. Inspect existing
   catalog, accounts, project and environment. Reuse approved objects; do not
   import/link/create everything just because the console is unfamiliar.
4. **Sandbox and cost:** establish exactly which objects may be created and
   later removed. A name like `hello-world` is not proof an existing object is
   disposable. Confirm new billable infrastructure and destructive teardown
   with exact targets/effects; a learning request does not authorize them.
5. **Configure and deploy:** use Raptor's current contracts for canonical names,
   cloud-account layers, required override-only fields, env readiness, plan,
   release scheduling and exact-ID monitoring. Saved config is not deployed.
   A module-authoring exercise is optional and separately scoped.
6. **Close the loop:** verify runtime outputs/health, ask the user to explain the
   key relationship in their words if useful, offer cleanup with its cost/retention
   consequences. Remove only sample objects created/authorized in this journey;
   don't remove shared catalogs or pre-existing infrastructure.

## Resume

The prior guide recorded `~/.praxis/onboarding-progress.json` as flow IDs plus
completed step indices. Treat it as a progress hint, not authority to execute a
mutation. Reconfirm console/project/environment and object ownership after a
profile change; old numeric steps may not match this consolidated journey.
When recording progress, use meaningful milestones and target identity, source
date and verification evidence. Never store credentials or claim completion for
an accepted-but-running job. Preserve unknown legacy records when updating.

## No forced setup detours

Do not require local cloud authentication for a gateway read. A provider bootstrap
that needs an operator can run in its cloud shell/approved host. Do not test secret
access merely because the user has approved a sandbox. Follow the Raptor import
route if onboarding is actually brownfield adoption, and
[Database](database-migration.md)/[Cache](cache-migration.md)/[Secrets](secrets-migration.md)
only if data must move. Explain the distinction before selecting operations.
