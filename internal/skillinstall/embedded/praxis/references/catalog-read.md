# Read existing ig catalogs

Use the six `praxis mcp ig` read tools for code/service/infra tracing. Queries run
server-side: **no local ig, graphify, checkout or `praxis ig sync` is required**.
Source inspection is optional when needed to validate code. Building/publishing
is a separate [Catalog onboarding](catalog-onboarding.md) workflow.

Start with `ig_list_catalogs`; there is no default catalog. Record its version
and members. The same repo can belong to multiple catalogs with different callers,
so an answer about coupling is catalog-relative.

| Tool | Purpose |
|---|---|
| `ig_list_catalogs` | available catalog names, versions and members |
| `ig_catalog --arg catalog=C` | cross-member topology, no member argument |
| `ig_explain` | node card with exact ID, source path/line, edges |
| `ig_impact` | downstream traversal, supports depth |
| `ig_query` | name-matched seeds followed by graph traversal; not semantic search |
| `ig_path` | concrete shortest path between two nodes |

The last four require both catalog and member. Select the lens deliberately:

```text
member=catalog  -> graph/service/route interfaces and cross-repo connections
member=<repo>   -> that code member's files/functions/symbols
member=infra    -> Facets module/datastore graph
```

```bash
praxis mcp ig ig_explain --arg catalog=CATALOG --arg member=catalog \
  --arg target='service:EXACT_ID' --json
```

These tools produce token-budgeted **text**, so JSON mode can retain an MCP
envelope; see [Gateway output](gateway-and-output.md). Use labels/substrings to
discover nodes, then copy full IDs to disambiguate follow-up. Wrong member/target
can produce “no node” without a fuzzy suggestion. Rediscover IDs per catalog;
the same physical service can have a different ID in another project.

`ig_query` is not an OR search across every word in a vague prompt. Prefer one
precise symbol/name. It has no query-depth flag; use supported relation/context
filters and budget. `ig_impact` does have depth. List-valued filters use a JSON
array; repeating an identical `--arg` key overwrites it.

For FE-to-BE tracing: topology -> shared route interface -> consumer call site
and producer handler in their respective members. `calls(http,N)` is indicative
rollup evidence, not a runtime trace. Internal impact on an HTTP entrypoint may
show no affected nodes; use the shared route and cross-member calls for external
consumers. Absence of an extracted edge does not prove no dependency.

Infra members may be coarse module-type graphs, not fully wired live topology.
For actual configured/runtime resources use the Raptor skill. To map a source
card to a local file: verified checkout + member subdirectory + source-relative
path. Re-anchor by symbol if local SHA differs from the build. An optional local
checkout note is only a hint; don't invent paths or require clones just to query.
