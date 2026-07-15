---
name: use-ig
description: Use when working in a project that is onboarded to `ig` (infragraphify) and you need to answer how repos/services connect, where the code for a concept or endpoint lives, how a frontend call reaches its backend handler, what infrastructure backs a service, or the blast radius of a change — instead of grepping a monorepo. Triggers on cross-service/cross-repo tracing, FE↔BE API coupling, code↔infra mapping, impact/what-breaks questions, "how does X flow through the system".
---

# Using ig to answer questions about a project

**Praxis-MCP read route — queries run server-side via `praxis mcp ig`; no local
`ig` needed.** (If praxis is unavailable, ig ships a native local-`ig` copy of
this skill instead. Only one is ever installed at a time.)

`ig` holds a prebuilt **catalog** for a project — a set of code and infra graphs
joined at their shared boundaries. When a question spans repos or services, or
asks "what connects to X / what breaks if I change X", query the catalog before
grepping source. Every read runs on the Praxis server under org-managed
credentials; your laptop needs no `ig` binary, no synced tree, no cloud secrets.

**The division of labor:** ig states facts (graphs, provenance, source
file+line). YOU decide, act, and ask the user. The MCP tools are read-only —
they never build, never prompt, never touch your working copy.

**Boundary — the four `praxis mcp ig` tools are the whole read interface.** Do
not reach for a local `ig`/`graphify` binary and do not try to `praxis ig sync`;
in this route reads execute server-side. (Building/refreshing a catalog is a
separate CI/setup concern that still uses `ig` on a builder host — not your job
here.)

## First: list the catalogs

There is **NO default catalog** and every read tool needs BOTH a `catalog` and a
`member`. Start here:

```
praxis mcp ig ig_list_catalogs
```

It returns the org's catalogs as `name + version`, each with its **members**.
Pick the `<catalog>` your question is about and pass it as `--arg catalog=<name>`
to every other tool. A repo can be a member of MORE THAN ONE catalog (e.g.
`control-plane` in both `capillary-cloud` and `saas-cp`); because `calls` edges
only appear between members present in the SAME catalog, "who calls
control-plane" is catalog-relative — ask each catalog.

## The `member` arg — pick your lens (REQUIRED)

A catalog is a **graph of graphs**, so a node is addressed *per member*. Every
read tool takes `--arg member=<m>` and it selects which graph you read:

| `member=` | You reach | Use for |
|---|---|---|
| **`catalog`** | the connective tissue: `service:*` / `route:*` / `graph:*` interface nodes + the cross-repo `calls` / `deploys_as` / `provisions` edges | "how do repos/services connect", "what calls X", frontend↔backend, code↔infra |
| **`<repo>`** (e.g. `control-plane`) | that repo's code graph (functions, files, symbols) | "where does the code for X live", per-repo blast radius |
| **`infra`** | the Facets infra graph (module types, datastores) | "what infra module backs X" |

The literal member name `catalog` is the graph-of-graphs itself — that is where
the interface/connection layer lives. `service:*`/`route:*` nodes are NOT inside
a repo member (`ig_explain --arg member=control-plane --arg target="service:x"`
returns "no node"); read them with `--arg member=catalog`.

## The catalog model — read this before running commands

```
  CATALOG  (one project)
    graph:control-plane-ui-react ──calls(http, 469)──▶ graph:control-plane   ← indicative connection
          │ deploys_as                                       │ deploys_as
          ▼                                                  ▼
     service:control-plane-react                    service:control-plane-service
          ▲ provisions                                       ▲ provisions
          └──────────────── graph:infra ─────────────────────┘

  MEMBERS  (each a full graph)
   ┌ control-plane-ui-react (code) ┐   ┌ control-plane (code) ┐   ┌ infra ┐
   │  .approveRelease() ─calls─▶    │   │  route:POST …/approve │   │ mongo │
   │  route:POST …/approveRelease ──┼───┼── handled_by ─▶ .approveRelease()  │ redis │
   └───────────────────────────────┘   └──────────────────────┘   └───────┘
                        ▲ the SAME route node is in both members: the off-page
                          connector that joins frontend call site → backend handler
```

- **member** — one repo's graph (`kind=code`) or the Facets infra graph (`kind=infra`).
- **graph node** (`graph:<member>`) — a whole member inside the catalog.
- **interface node** — a shared boundary: `service:*` (a Facets service), `route:*`
  (an HTTP endpoint), `queue:*` (a queue/topic), `pkg:*` (a package).
- **connection** — a typed cross-member edge: `deploys_as` (code → its service),
  `provisions` (infra → a service), `provides`/`consumes` (a member ↔ an interface).
- **calls edge** — `graph:A ──calls(http, N)──▶ graph:B`: indicative "A calls B
  over HTTP N times", rolled up from shared `route:` nodes. The concrete link is
  the route node itself, present in both members (frontend `calls` it, backend is
  `handled_by` it).

Two caveats seen in practice: a `service:*` id for the same physical service can
be spelled differently across catalogs (`service:control-plane-service` in one vs
`service:control-plane` in another — they mirror each project's infra naming), so
rediscover it per catalog rather than reusing an id; and a wrong `target` yields
a plain "no node" with no fuzzy suggestion.

## Command surface (the four `praxis mcp ig` tools)

Every read tool takes `--arg catalog=<c>` (from `ig_list_catalogs`), `--arg
member=<m>` (the lens — see above), and `--arg target=<node>`. Output is
**token-budgeted TEXT** written for an LLM to read.

```
praxis mcp ig ig_list_catalogs                                                            → the org's catalogs (name + version + members); START HERE
praxis mcp ig ig_explain --arg catalog=<c> --arg member=<m> --arg target=<node>           → node card (source file+line, edges) — the workhorse
praxis mcp ig ig_impact  --arg catalog=<c> --arg member=<m> --arg target=<node> [--arg depth=N]   → what a change to <node> reaches downstream
praxis mcp ig ig_query   --arg catalog=<c> --arg member=<m> --arg target="<terms>" [--arg depth=N] → BFS from name-matched start nodes (NOT semantic search)
```

- `ig_explain` is the workhorse: it lands on a node and prints its source
  file+line, degree, and edges. Reach for it to locate a symbol (in a `<repo>`
  member) or an interface node (in the `catalog` member).
- `ig_impact` traverses code edges downstream from `target` — "what does a change
  to this node reach". `depth` bounds the walk.
- `ig_query` seeds from name-matched start nodes and walks — see the gotcha below.

## Recipes

**How do the repos/services connect?** Read the `catalog` member —
`ig_explain --arg member=catalog --arg target="graph:<repo>"` shows a repo's
`calls` / `deploys_as` edges to other members; `ig_query --arg member=catalog
--arg target="<name>"` walks the interface layer.

**Trace a frontend call to its backend handler.** `ig_explain --arg
member=catalog --arg target="service:<name>"` (or the `route:` node) to see the
cross-repo `calls` edge, then drop into the backend `<repo>` member —
`ig_explain --arg member=<be-repo> --arg target="<handler-symbol>"` — to land on
the handler. The interface node in the `catalog` member is the join.

**What breaks if I change a symbol?** `ig_impact --arg member=<repo> --arg
target="<symbol>"` for internal downstream. For an HTTP endpoint, also
`ig_explain --arg member=catalog --arg target="route:<METHOD /path>"`: its
cross-member `calls` edge is the real coupling (a route/contract change forces the
OpenAPI client + its callers to change), which `ig_impact` alone won't show.

**What infra backs a service?** Read the `provisions` connections on the
`service:*` node in the `catalog` member (`ig_explain --arg member=catalog --arg
target="service:<name>"`), then enumerate the `infra` member with `ig_query --arg
member=infra --arg target="<resource>"`. Infra nodes are Facets module types
(`service/*`, `s3/*`, `mongo`, `artifact/*`) and are often coarse/degree-0 —
expect module-type + datastore names, not a fully wired topology.

**Where does the code for a concept live?** `ig_explain --arg member=<repo>
--arg target="<name-or-substring>"` to land on a node, then follow its edges —
more reliable than `ig_query` for locating a symbol. The node card's source
file+line is relative to that member's repo root.

## Gotchas (these are where agents lose time)

- **`ig_query` is BFS traversal, not semantic search.** It picks a small set of
  best-name-match start nodes (not an OR over every word in the term string), then
  walks edges. A vague term (`"release"`) starts inside an unrelated node and
  wanders. Seed with ONE specific symbol, or skip it and `ig_explain` the node
  directly.
- **Wrong `member` = "no node", even for the right target.** Interface nodes
  (`service:*`, `route:*`, `graph:*`) live ONLY in the `catalog` member; code
  symbols live only in their `<repo>` member. Asking for a `service:*` in a repo
  member, or a code symbol in `catalog`, returns "no node". If a target you're
  sure exists isn't found, you're probably in the wrong lens — switch `member`
  (`catalog` for connections, `<repo>` for code, `infra` for modules).
- **A wrong `target` returns "no node" with NO fuzzy suggestion.** Don't guess ids
  blindly — start from `ig_list_catalogs`, then `ig_explain` a label/substring you
  are confident about, and copy the exact `ID:` it prints for follow-up calls.
- **Reference a node by label/substring first; use the full ID to disambiguate.**
  A unique label works (`"approveRelease"`, or a route label `"POST
  …/approveRelease"`). When several nodes share a label, `ig_explain` may
  auto-pick one; copy the full `ID:` line from its output and pass THAT to
  `ig_impact` to be unambiguous.
- **`ig_impact` on an HTTP entry point returns "No affected nodes" — that IS the
  answer:** the endpoint has no internal callers because it's invoked over HTTP.
  `ig_impact` traverses code edges; it is not an HTTP blast-radius oracle. Use the
  `route:` node + `calls` edge for cross-member coupling.
- **Node ids differ by member kind** — code = a `path_symbol` slug
  (`v2_src_..._approverelease`), infra = module-type paths (`s3/default/0.2`,
  `service/deployment`, `mongo`, `artifact/*`). Both are accepted as `target`.

## Worked example (real: capillary-cloud)

```
praxis mcp ig ig_list_catalogs
  → capillary-cloud 2026.07.06-071024  members: control-plane, control-plane-ui-react, infra
    saas-cp 2026.07.05-…               (pick capillary-cloud)
praxis mcp ig ig_explain --arg catalog=capillary-cloud --arg member=catalog \
  --arg target="service:control-plane-service"
  → Type: service   <-- infra [provisions]   --> control-plane [deploys_as]   (the join)
praxis mcp ig ig_explain --arg catalog=capillary-cloud --arg member=control-plane \
  --arg target="approveRelease"
  → .approveRelease()  UiDeploymentController.java:L449   Degree 6   (backend handler)
praxis mcp ig ig_impact  --arg catalog=capillary-cloud --arg member=control-plane \
  --arg target="approveRelease"
  → the controller is an HTTP entry point, so internal downstream is minimal — the
    real blast radius is the shared route: node + the calls edge (a contract change
    forces the OpenAPI client + its callers to change).
```

The shared `route:` node answers "which frontend calls which backend"; the
`service:*`/`provisions` connections answer "what infra backs it".
