# Web component transport and UI contract

Browser requests use the **viewer's session**, not the agent's PAT. Prefer
same-origin relative CP URLs with cookies. The preserved
[transport](../assets/web-component-template/src/transport/cp.js) derives the current
origin; never override it with another tenant, bake in a host/token, or disable
permission checks. The host's `user` attribute is display/context, not trusted
authorization. Handle 401/403 distinctly from empty data.

## Critical transport boundaries

- Tunnel traffic uses `baseClusterId || clusterId` for dependent environments.
  Fetch cluster metadata before constructing the tunnel URL.
- Use a hard timeout (about 15s) for tunnel reads; a dead tunnel can otherwise
  exhaust the browser connection pool. Propagate cancellation on scope/unmount.
- Serialize `/cc-ui/v1/.../k8s-explorer/*` via a single-slot queue; concurrent
  explorer reads have failed in the observed platform. A failed read must not
  wedge the queue. Other independent CP reads can be parallel.
- Avoid caching a failed scope lookup as verified base-cluster identity. The
  preserved template does not yet enforce this: non-2xx metadata becomes null
  and caches the original cluster ID; only thrown failures evict the entry.
  Metadata lookup also lacks timeout/cancellation. Before using this scaffold
  for dependent-env data, add failing response/timeout/retry tests and fix the
  lookup to fail visibly. Never present fallback routing as verified identity.
  Support refresh/retry when environment configuration changes.

## Discover contracts, not guessed URLs

Use the selected platform's current `/v3/api-docs` plus actual read responses.
An HTTP 200 may be SPA HTML, not an API JSON response (`/v2/api-docs` has behaved
that way). Verify content type/shape. Internal/undocumented destructive siblings
are not a substitute for a missing public API. Agent-side Facets access follows
the Raptor skill; never scrape the first token from an INI file.

Useful source-verified starting paths (verify deployed shapes):

| Read | Path / distinction |
|---|---|
| Projects | `/cc-ui/v1/stacks/` |
| Env picker | `/cc-ui/v1/stacks/{stackName}/clusters-metadata` |
| Env details | `/cc-ui/v1/clusters/{clusterId}` (baseClusterId, namespace) |
| Env releases | `/cc-ui/v1/clusters/{clusterId}/deployments`, not project-scoped |
| Capability metadata | `/cc-ui/v1/clusters/{clusterId}/resourceDetails` |
| Rules + firing/silenced state | `/cc-ui/v1/clusters/{clusterId}/alerts` |
| Firing-only | `/cc-ui/v1/clusters/{clusterId}/open-alerts`; not rule definitions, not supported everywhere |
| Explorer | `/cc-ui/v1/clusters/{clusterId}/k8s-explorer/...` |

Explorer kinds include pods, deployments, statefulsets, replicasets, daemonSets,
services, ingresses, hpa, pvc/pv, jobs/cronJobs and scoped events/logs. Avoid
secret/config value dumps. Its OpenAPI-required `labels` map has been a generator
artifact: don't fabricate a labels query if the endpoint accepts an unfiltered
read. Use actual schema/response tests for the deployed version.

Blueprint `content` and `override` fields are separate; raw content is not the
environment's effective config. Raptor owns merge/override interpretation.
Rule definitions and firing state are also separate. Never mutate silence/rules
just to populate a read-only panel; require the separate permission and workflow.

The reviewed tunnel accepts Grafana, not a guessed `/tunnel/ID/prometheus` route.
Metrics access/capability discovery needs a verified datasource proxy contract.
Grafana Live websocket upgrades may be unsupported (501); don't build streaming
panels on an assumed websocket path. `?refresh=true` is a platform cache escape
hatch only where documented, not a general credential/permission workaround.

## Navigation

Dispatch `facets-navigate` from the component with `bubbles:true`, `composed:true`
and `detail:{route,queryParams}`. Without composed it stops at the shadow boundary.
Use verified routes; don't reach into the host router or assign window.location
to an invented path. Registration scope/page context beats contextualAttributes.
Raptor's UI navigation route owns human-facing links outside the component.
