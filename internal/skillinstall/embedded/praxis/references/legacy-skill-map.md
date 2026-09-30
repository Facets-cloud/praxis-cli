# Legacy global skill names

Use this only when an old task packet, hook or conversation names a replaced
global skill. These are routes, not independent skill entrypoints. Organization/
personal namesakes can contain additional customer policy; preserve and inspect
their provenance instead of ignoring them based on the same spelling.

## Folded into Praxis

Server names were installed with an extra `praxis-` prefix (including the doubled
`praxis-praxis-dag` and `praxis-praxis-dag-runner`). Both spellings route here:

| Old source name | Current reference |
|---|---|
| cloud-operations | [Cloud operations](cloud-operations.md) |
| k8s-operations | [Kubernetes](kubernetes-operations.md) |
| newrelic-operations | [New Relic](newrelic-operations.md) |
| cloud-waste-finder | [Cloud waste](cloud-waste.md) |
| aws-change-audit | [AWS change audit](aws-change-audit.md) |
| db-migrate | [Database migration](database-migration.md) |
| cache-migrate | [Cache migration](cache-migration.md) |
| secrets-migrate | [Secrets migration](secrets-migration.md) |
| custom-agents-operations | [Agents](agents.md) |
| duties-operations | [Duties](duties.md) |
| learning | [Learning](learning.md) |
| build-web-component | [Web components](web-components.md), design/transport and template assets |
| onboard-ig | [Catalog onboarding](catalog-onboarding.md), distributed CI/extractors |
| praxis-dag | [Task DAG](task-dag.md) |
| praxis-dag-runner | [DAG runner](task-dag-runner.md) |
| slack-progress-tracker | [Slack progress](slack-progress.md) |

Former CLI embedded entries:

| Entry | Current reference |
|---|---|
| praxis | this package's root + [Access](context-and-access.md)/[Output](gateway-and-output.md) |
| praxis-getting-started | [Access](context-and-access.md) |
| praxis-memory | [Memory](memory.md) |
| praxis-onboarding | [Onboarding](onboarding.md) |
| use-ig (CLI variant) | [Catalog reads](catalog-read.md) |

## Owned by Raptor

Load the **raptor skill** and follow its named task route; don't revive the old
prefixed instructions. Names below refer to exact replaced GLOBAL definitions.

| Old source name | Raptor subject |
|---|---|
| audit-facets-blueprint | blueprint audit |
| build-facets-module | modules, schema/UI contracts, QA |
| design-facets-module | module design reasoning |
| docs-helper | current task-specific reference/documentation discovery |
| facets-blueprint | configuration, lifecycle, Git-first bulk blueprints |
| facets-ci | artifacts, service mapping, builds/delivery |
| facets-gcp-zero-change-import | imports + GCP isolation |
| facets-notifications | notifications |
| module-actions | action authoring/execution |
| modules-repo-workflow | module repository, contract version/soft revision |
| release-debugging | release debugging, scheduling, ZIP revision correlation |
| terraform-import | import selection and advanced state operations |
| zero-change-import | plan-first adoption without cloud changes |
| facets-module-testing | module QA/preview |

Pod-only global skills (e.g. incident operations, agent delegation, hosted use-ig,
observability dashboards and skill-builder) are not implicitly bundled here.
Their existing hosted delivery remains distinct. If a task needs an unavailable
capability, report that boundary rather than inventing a tool or copying a
server-only execution recipe into the CLI workflow.
