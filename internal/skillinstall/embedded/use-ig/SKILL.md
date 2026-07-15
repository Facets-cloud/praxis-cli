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

**Boundary — the six `praxis mcp ig` tools are the whole read interface.** Do
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

## Command surface (the six `praxis mcp ig` tools)

Every read tool takes `--arg catalog=<c>` (from `ig_list_catalogs`) and, except
`ig_list_catalogs`/`ig_catalog`, `--arg member=<m>` (the lens — see above).
Output is **token-budgeted TEXT** written for an LLM to read.

```
praxis mcp ig ig_list_catalogs                                                             → the org's catalogs (name + version + members); START HERE
praxis mcp ig ig_catalog  --arg catalog=<c>                                                → topology MAP: members + service:* interfaces + deploys_as/provisions/calls edges
praxis mcp ig ig_explain  --arg catalog=<c> --arg member=<m> --arg target=<node>           → node card (source file+line, edges) — the workhorse
praxis mcp ig ig_impact   --arg catalog=<c> --arg member=<m> --arg target=<node> [--arg depth=N] [--arg relation=R] [--arg context=C]  → what a change to <node> reaches downstream
praxis mcp ig ig_query    --arg catalog=<c> --arg member=<m> --arg query="<free text>"  [--arg relation=R] [--arg context=C]           → BFS from name-matched start nodes (NOT semantic search)
praxis mcp ig ig_path     --arg catalog=<c> --arg member=<m> --arg from_node=<a> --arg to_node=<b>                                     → shortest path between two nodes, hop by hop
```

- **`ig_query` takes FREE TEXT.** `--arg query="where does approveRelease reach
  the DB"` or a symbol name — ig seeds the BFS from the name-matched start nodes.
  It has NO `depth` (ig hardcodes query depth); scope it instead with `--arg
  relation=calls` / `--arg context=call` (repeat by passing the arg again) or cap
  output with `--arg budget=N` (tokens). `ig_impact` takes the same filters plus
  a real `--arg depth=N`.
- `ig_catalog` is the map — START HERE for "how do these repos/services connect".
  It needs no `member` (it IS the cross-member view).
- `ig_path` answers "how does A reach B" with the concrete hop-by-hop edge chain.
- `ig_explain` is the workhorse: it lands on a node and prints its source
  file+line, degree, and edges. Reach for it to locate a symbol (in a `<repo>`
  member) or an interface node (in the `catalog` member).
- `ig_impact` traverses code edges downstream from `target` — "what does a change
  to this node reach". `depth` bounds the walk.
- `ig_query` seeds from name-matched start nodes and walks — see the gotcha below.

## Recipes

**How do the repos/services connect?** `ig_catalog --arg catalog=<c>` — the
one-shot topology map (members + `service:*` interfaces + `deploys_as` /
`provisions` / `calls` edges). Start here, then drill into a specific interface
with `ig_explain --arg member=catalog --arg target="service:<name>"`.

**Trace a frontend call to its backend handler.** From the `ig_catalog` map (or
`ig_explain --arg member=catalog --arg target="service:<name>"` / the `route:`
node) find the cross-repo `calls` edge, then drop into the backend `<repo>`
member — `ig_explain --arg member=<be-repo> --arg target="<handler-symbol>"` — to
land on the handler. The interface node in the `catalog` member is the join.

**How does A reach B?** `ig_path --arg member=<repo> --arg from_node=<a> --arg
to_node=<b>` — the concrete hop-by-hop edge chain between two symbols (or "no
path"). Faster than eyeballing `ig_impact` output when you have both endpoints.

**What breaks if I change a symbol?** `ig_impact --arg member=<repo> --arg
target="<symbol>"` for internal downstream (scope with `--arg relation=calls` or
`--arg depth=N`). For an HTTP endpoint, also `ig_explain --arg member=catalog
--arg target="route:<METHOD /path>"`: its cross-member `calls` edge is the real
coupling (a route/contract change forces the OpenAPI client + its callers to
change), which `ig_impact` alone won't show.

**What infra backs a service?** Read the `provisions` connections on the
`service:*` node in the `catalog` member (`ig_explain --arg member=catalog --arg
target="service:<name>"`), then enumerate the `infra` member with `ig_query --arg
member=infra --arg query="<resource>"`. Infra nodes are Facets module types
(`service/*`, `s3/*`, `mongo`, `artifact/*`) and are often coarse/degree-0 —
expect module-type + datastore names, not a fully wired topology.

**Where does the code for a concept live?** `ig_query --arg member=<repo> --arg
query="<free-text question>"` to BFS from name-matched nodes, or `ig_explain
--arg member=<repo> --arg target="<name-or-substring>"` to land on one node and
follow its edges (more precise when you know the symbol). The node card's source
file+line is relative to that member's repo root.

## Resolving a node to a local file

A node card's `source file:line` is **relative to the member's build root** —
which for a monorepo member is a *subdirectory*, not the git repo root. To open
it:

- **Find the checkout.** If cwd is inside the repo, `git rev-parse --show-toplevel`
  is the root; otherwise look under the usual places (`~/src`, `~/work`,
  `~/praxis-envs/<profile>/…`). If it isn't cloned, say so — don't invent a path.
- **Prepend the member's subdir:** `abs = <checkout>/<member-subdir>/<file>` —
  `.` for a whole-repo member, e.g. `services/billing` for a monorepo one (find
  it by locating the node's `file` under the checkout).
- **Mind drift.** The catalog was built at a past commit; if the checkout moved
  on, treat `L<n>` as approximate and re-anchor by the symbol name ig printed.

To avoid re-finding a checkout, you may jot it in `~/.praxis/ig-checkouts.json`
(your own notes, e.g. `"<catalog>/<member>": {repo, path, dir}`) — an optional
convenience, nothing depends on it being present or correct.

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
