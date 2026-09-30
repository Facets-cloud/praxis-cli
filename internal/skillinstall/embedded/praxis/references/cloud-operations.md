# Cloud operations

Use `cloud_cli` for organization-managed AWS/GCP/Azure discovery. First discover
`list_cloud_integrations`, then select the integration's name, provider and
account/project identity. A region default is not evidence all regions were
visited. Name the account and requested region on each call.

```bash
praxis mcp cloud_cli list_cloud_integrations --json
praxis mcp cloud_cli run_cloud_cli --json \
  --arg integration_name=ACCOUNT --arg region=REGION \
  --arg command='rds describe-db-instances --output json'
```

Provider prefix is omitted: `ec2 describe-instances`, `compute instances list`,
`network vnet list`, not `aws ...`, `gcloud ...`, `az ...`. The gateway selects
the executable and credentials. Do not try to override its credentials/profile
through provider flags. Shell operators, command substitution and remote-file
assumptions are not supported. Use explicit gateway filter parameters where
present; preserve pagination metadata when filtering.

## Account layers and effects

Praxis integration access is not a Facets deployment account or blueprint
`cloud_account` resource. Raptor owns deployment account/configuration workflows.
`list_facets_accounts` discovers importable links; `import_facets_account` and
`sync_facets_accounts` **write integration records**. They may be idempotent, but
are not obligatory read-only preflight. Use them when linking/syncing is in scope,
then list/read back what changed.

Most provider verbs are read-restricted, but the current gateway explicitly
allows **AWS `ssm send-command`**. It executes remotely and may mutate systems;
inspect document, instances, parameters and impact before an authorized call.
Retain its command ID and verify invocation results; do not retry on timeout
without inspecting whether execution already started. Availability is not approval.

Some allowed AWS reads write provider output to a file (`get-object`, exports,
SDK download). The gateway requires `/dev/stdout` as the destination for those
calls. Do not infer read-only data sensitivity from a `get` prefix: object bodies
and logs can contain secrets. Request the smallest data needed.

## Evidence

Prefer focused inventory -> related network/IAM/storage -> bounded metrics/logs.
Cloud inventory returns configuration, not prices, workload health or ownership.
Failed permission checks are unscanned areas. State account, regions, window,
pages completed, sampling and failed reads when presenting a report.

Use [Cloud waste](cloud-waste.md) for cost hypotheses, [AWS change audit](aws-change-audit.md)
for attribution, and [Kubernetes](kubernetes-operations.md) for workload evidence.
Approved migration engines on a reachability host are a separate access path;
do not generalize “gateway credentials” into “no operator may use local tooling”.
