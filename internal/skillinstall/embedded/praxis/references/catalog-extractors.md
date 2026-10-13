# Cross-graph extractors

A catalog joins opposite sides of the **same canonical interface key**, not
similar-looking service names. An unmatched consumer route is a dangling
interface, not proof the backend is absent.

Built-in route passes include adapters for Spring mappings, OpenAPI TypeScript
clients, FastAPI, supported Python clients and queue conventions. Check installed
ig's adapter output first; language support evolves. A missing edge can mean a
wrong catalog/member, disabled routes, unmatched naming, stale graph or missing
adapter. Don't add an extractor until evidence identifies the missing convention.

Use the installed contracts:

```text
ig extractor spec
ig extractor scaffold NAME -kind KIND
ig extractor test "python3 .ig/extractors/NAME.py" MEMBER_SOURCE
```

Scaffold/test before declaring it in the manifest's `connections.extractors`.
These commands execute code locally; use a reviewed project-local script, never
an untrusted downloaded shell command. It needs no ig binary rebuild.

Typical stdout is a JSON array of hits:

```json
[{"key":"queue:orders","dir":"out","symbol":"Orders.publish","file":"src/orders.py","line":42}]
```

`out` is caller/producer; `in` is handler/consumer. Source paths are relative to
the member's source root, and symbols must resolve to actual graph nodes.
Normalize identities consistently on both sides, including route params/prefixes
and queue/topic names. Test producer and consumer fixtures, mismatched names,
missing symbols and deleted interfaces; contract-valid JSON alone does not prove
the resulting edges are semantically correct.

Declare name/cmd/kind using the scaffold's current stanza; run `ig routes -p C`
or the supported route-aware build, then inspect exact interfaces and rollup
edges. Keep extractors source-local or explicitly catalog-owned; distributed CI
must reproduce their relative execution paths. See [Distributed CI](catalog-distributed-ci.md).
Do not silently author/publish repo files when the user only asked why an edge
was missing.
