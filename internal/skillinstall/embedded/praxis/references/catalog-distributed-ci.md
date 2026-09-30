# Distributed ig CI

Choose central builds when one approved job can access all source repos;
distributed builds when each source must remain in its own CI/security boundary.
Do not modify ordinary application CI merely to install graph publishing.

```text
Repo A CI -> own member graph --+
Repo B CI -> own member graph --+-> Praxis catalog join -> MCP readers
Facets infra refresh ----------+
```

Each repo builds with `ig member build`, which preserves canonical interface
nodes. Bare graphify output lacks the overlay needed for cross-member joins.
Assembly uses member graphs, not source checkouts or a server LLM key.

## CI contract

Inspect the actual default branch and existing workflows. For optional catalog
CI, add a separate narrowly scoped workflow (push to that default branch and
manual dispatch); don't make a graph failure a new PR merge gate without request.
Gate missing credentials/no claims as **skipped**, never claim a skipped graph was
published. When a publish was attempted, surface its real failure.

Use the approved CI authentication method for that deployment. A Praxis API key
and a Facets PAT are not interchangeable; verify `praxis login --help` and the
CI principal's scope. Keep secrets out of generated workflow literals and logs.
Public binary releases avoid distributing access to the private ig source.
Use pinned, reviewed release/tool versions according to repository policy.

Discover claiming catalogs with `praxis ig claims --git CANONICAL_URL`; a repo can
be claimed more than once. Pull each manifest with `praxis ig manifest pull C`.
Build from that checkout's exact commit; preserve `git`, `sha`, member subdir and
content digest. A manifest can point to code plus extractors; review executable
extractors as code, not harmless configuration.

For several catalogs, `ig member build MEMBER -manifest A -manifest B -src .
-out BUILD_ROOT` can label code once, then embed each catalog's distinct overlay.
Use current help to choose output/seed/label flags. Single catalog output is
`BUILD_ROOT/member/MEMBER/...`; multiple catalogs use a per-catalog build root.
Publish each using **that root**, not `.../member/MEMBER`:

```bash
praxis ig publish BUILD_ROOT/CATALOG --catalog CATALOG --member MEMBER
```

## Labels, portability and freshness

LLM community labels are optional. Without a key, a structural graph remains
useful; don't fail publishing solely because it has placeholder labels. Keys
belong in the source CI only; labeling can send symbol/file names to a model.
Choose provider/model with that data policy in mind. graphify's Python package
is `graphifyy`; provider SDK extras may be needed (`--with openai` in the reviewed
OpenAI setup). A zero exit does not prove LLM labels ran: inspect provenance.

Prior published graphs/label sidecars can seed incremental labeling. Existing
names should survive keyless refresh; use supported seed metadata instead of
assuming a catalog summary SHA proves member freshness. Interface overlays must
be stripped before code reanalysis and rebuilt afterward so deleted routes don't
linger as phantom edges. Preserve canonical join keys and portable relative paths.

`.graphifyignore` adds exclusions on top of `.gitignore`; it cannot re-include
already excluded files. Propose/review it as a source-repo change, not a universal
list that drops migrations/tests regardless of their importance.

Source-local extractors live in that repo. Catalog-owned extractors must travel
with their manifest and be mirrored into the expected working directory before
build. Confirm the CLI/server actually support those companion files; manifest
text publication alone is not proof they were transferred.

Unchanged member/catalog output is healthy idempotence. Verify actual published
member Git/SHA and joined topology; a stored infra-only catalog is not full code
coverage. Changes to source CI, secret configuration, manifests and publication
are separately scoped writes. Keep optional readers free from builder setup.
