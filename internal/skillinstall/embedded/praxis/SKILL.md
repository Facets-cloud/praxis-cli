---
name: praxis
description: Use when working with the Praxis CLI, organization cloud or Kubernetes integrations, New Relic, organization memory, duties, custom agents, task DAGs, ig catalogs, data or secret migrations, Slack progress, or building Facets web components. Facets configuration, modules, imports and releases belong to the separate raptor skill.
metadata:
  version: "1.0"
---

# Praxis

Use Praxis to reach organization-managed capabilities from a local AI host.
This is the consolidated CLI skill. Load its relevant references instead of
the replaced global `praxis-*` skills. Organization/customer-specific skills
remain complementary; a prefix alone does not make a skill obsolete.

## Mental model

```text
Local AI host
  +-- praxis CLI -- selected identity --> Praxis gateway
  |                                       +-- cloud / Kubernetes / New Relic
  |                                       +-- memory / duties / agents / DAG
  |                                       +-- ig catalogs / artifacts / Slack
  +-- raptor CLI -----------------------> Facets control plane
  +-- source checkout / approved runner -> code, builds, migration engines

Gateway identity != cloud integration != Facets deployment account
Configuration != running state != successful operation
```

## Start at the task boundary

For live Praxis work, check `praxis status --json`: selected profile, URL,
authentication and installed capabilities. Do not read credential files into
the transcript. Existing context may already establish the target; ask only
for missing scope that changes the work. Read-only repository work needs no login.

Discover deployed functions with `praxis mcp --json`; invoke them as
`praxis mcp <namespace> <function>`. A cached manifest is a hint, not proof of
current availability. Use `--help` for installed syntax and the references for
meaning. Tool availability never implies permission to execute a mutation.

Use local `raptor` and its **raptor skill** for Facets operations, never a
guessed `praxis mcp raptor` wrapper. Read that skill's task route before selecting
an import/release/configuration command. If it is unavailable, name the missing
dependency; do not substitute an obsolete prefixed recipe. Non-Facets work can
continue without it. This package targets CLI hosts, not the hosted pod harness.

## Unfold only what the task needs

Read the matching reference fully before acting. Add a second reference only
when the task crosses that boundary; do not preload this directory.

| Task | Read |
|---|---|
| Setup, login, profile selection, shared Raptor credentials, local scope | [Context and access](references/context-and-access.md) |
| MCP discovery, arguments, output/envelopes, errors, long-running calls | [Gateway and output](references/gateway-and-output.md) |
| Find/access a repo; Git credentials; bulk blueprint work | [Repository access](references/repository-access.md) |
| Skill source, refresh, compatibility, old names or rollback | [Skill lifecycle](references/skill-lifecycle.md) |
| Recall/list/save memory; user vs organization visibility | [Memory](references/memory.md) |
| First hands-on journey or resume onboarding | [Onboarding](references/onboarding.md) |
| Explain a concept, training or optional quiz | [Learning](references/learning.md) |
| AWS/GCP/Azure discovery, account linking or SSM boundaries | [Cloud operations](references/cloud-operations.md) |
| Pod/container failures, events, Helm history, service connectivity | [Kubernetes operations](references/kubernetes-operations.md) |
| NRQL, APM, logs, entities or New Relic alerts | [New Relic](references/newrelic-operations.md) |
| Idle resources, cost/waste assessment | [Cloud waste](references/cloud-waste.md) |
| CloudTrail, ClickOps, tool overlap, change attribution | [AWS change audit](references/aws-change-audit.md) |
| Move relational data / CDC / DB cutover (not IaC adoption) | [Database migration](references/database-migration.md) |
| Redis/Valkey copy, mirror, warmup or cutover | [Cache migration](references/cache-migration.md) |
| AWS-to-GCP secret values, ESO mapping/read-back | [Secrets migration](references/secrets-migration.md) |
| Create/update organization agent definitions | [Agents](references/agents.md) |
| Recurring duty, trigger, run history, reports or findings | [Duties](references/duties.md) |
| Durable multi-step run, work packet, independent judge or human gate | [Task DAG](references/task-dag.md) |
| Explicitly requested unattended DAG driving | [DAG runner](references/task-dag-runner.md) |
| Trace code/services/infra or blast radius using existing ig catalogs | [Catalog reads](references/catalog-read.md) |
| Build/enrich/publish an ig catalog; repo-to-service mapping | [Catalog onboarding](references/catalog-onboarding.md) |
| Per-repo graph CI, incremental labels, portable publication | [Distributed catalog CI](references/catalog-distributed-ci.md) |
| Missing cross-repo edges or custom route/queue extractors | [Catalog extractors](references/catalog-extractors.md) |
| Publish a document, versioned evidence or human UI handoff | [Artifacts and UI](references/artifacts-and-ui.md) |
| Post/update an agreed Slack progress tracker | [Slack progress](references/slack-progress.md) |
| Build a Facets React/AntD custom element | [Web components](references/web-components.md) |
| Theme, shadow root, five UI states, bundling | [Web component design](references/web-component-design.md) |
| Viewer-session API calls, dependent-env tunnels, explorer queue | [Web component transport](references/web-component-transport.md) |
| An old task packet refers to a retired global skill | [Legacy skill map](references/legacy-skill-map.md) |

## Working contract

Keep the requested outcome, target and effects distinct. Audit/diagnose does not
authorize a repair, account connection, publication, message, schedule or release.
Do not switch shared defaults, upgrade tools or refresh installed skills merely
to get past a read failure. When an operation is asynchronous, retain its returned
identity and verify that operation; a timeout is not proof it stopped.

Report what was inspected/changed, the target, evidence and uncertainty. Failed
access is not absence; truncated pages are not complete inventories; an accepted
write is not runtime success. Source-derived baseline: 2026-09-10. Re-check help
and deployed contracts where versions differ. Keep secrets out of inputs to
logging tools, generated commands, committed files and user-facing output.
