# Source preservation and correction ledger

Canonical package: internal/skillinstall/embedded/praxis. All source reads use
Agent Factory de9d8850 or Praxis CLI 5663237, not their older parent checkouts.

| Source | Preserved in | Deliberate correction |
|---|---|---|
| CLI praxis / getting-started | root, context-and-access, gateway-and-output, repository-access, skill-lifecycle | current shared credentials; no auto-login/switch; exact MCP body/output |
| CLI praxis-memory | memory | pagination; audience explicit, org facts don't auto-authorize sharing |
| CLI praxis-onboarding + first-deployment flow | onboarding + Raptor first-deployment handoff | no compulsory module publish, guessed sample ownership, stale catalog_ops/import flags or text-grep release polling |
| CLI use-ig | catalog-read | reader/build separation; same-key --arg overwrites, not filter append |
| cloud-operations | cloud-operations | sync writes integrations; SSM exception; file-output distinction |
| k8s-operations | kubernetes-operations | no guessed prod target, env dumps, blanket exec safety or blocked rollout recipes |
| newrelic-operations | newrelic-operations | mutable subcommands and server output_file explicit |
| cloud-waste-finder | cloud-waste | current pricing evidence, partial scan, no safe-delete claim |
| aws-change-audit | aws-change-audit | preserve regional pagination, sample saturation, attribution and report outputs; optional bounded independent review instead of obligatory personas |
| db-migrate | database-migration, scripts/database | draft-only generator; single DB/schema, missing checksum/engine/TLS coverage explicit |
| cache-migrate | cache-migration, scripts/cache | type/size-only monitor is not a cutover gate; no value/TTL/lag completeness claim |
| secrets-migrate | secrets-migration, scripts/secrets | actual nonflat behavior; read-back permissions; short digest/latest concurrency limits |
| custom-agents-operations | agents | server definition vs installed file vs running session; preserve list replacement/ownership |
| duties-operations | duties | native run is lookup, MCP trigger async/paused, limits/timezone |
| learning | learning | optional teaching/quiz; no mandatory old docs-helper dependency |
| build-web-component | web-components, design, transport, assets/web-component-template | registration delegated to Raptor; no credential extraction or missing sibling refs; absent/error/empty distinctions |
| onboard-ig + distributed-ci + cross-graph-wiring | catalog-onboarding, catalog-distributed-ci, catalog-extractors, scripts/catalog, assets/catalog/.ig-version | current publish root/manifest flag; raw helper isn't effective-env artifact truth; version file retained as source provenance, not an automatic tool pin |
| praxis-dag + agents-md-block | task-dag | complete current packet and display_name; no extra installed root instructions needed |
| praxis-dag-runner | task-dag-runner | host-neutral monitoring; no mandatory Flow/ask-mac; reconcile ambiguous completion |
| slack-progress-tracker | slack-progress | complete body stdin, modern response, authorization and no automatic bot invitation |
| 14 Facets GLOBAL definitions | independent Raptor skill, legacy-skill-map table | canonical Raptor already reviewed; do not re-copy competing recipes |

Supporting resources copied byte-for-byte initially: database plan/verify/spec,
cache plan/monitor/spec, secrets replicate/mapping, catalog facets reader, nine
web-component template files (18 files byte-identical in final comparison).
Hidden .ig-version value preserved with a normalized trailing newline. Agent Factory seed
definitions remain in place for old clients and hosted pods.

The old audit personas, chapter ceremony and AGENTS.md force-load block were
consolidated into task-specific decision/verification guidance; they are not
additional discovered skill entrypoints. Customer-specific skills are not mapped
as obsolete. Script/template preservation does not imply live production QA.
