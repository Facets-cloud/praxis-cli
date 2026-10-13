# AWS change audit

Answer: which tools changed infrastructure, what they actually manage, where
ownership overlaps, and what is manual/ClickOps. Use [Cloud operations](cloud-operations.md)
for read access; never modify CloudTrail/IAM/resources to make an audit succeed.

## Query and coverage contract

CloudTrail `lookup-events` is regional, supports at most **one** lookup attribute
per call and does **not** support `userAgent` as that attribute. Fetch by
`ReadOnly=false`, EventName, ResourceName or another supported key; decode the
nested event JSON and classify userAgent/identity locally. State exact UTC window
and regions. Preserve each response's continuation token until pagination ends
or an explicit sample boundary is reached.

```bash
praxis mcp cloud_cli run_cloud_cli --json \
  --arg integration_name=ACCOUNT --arg region=REGION \
  --arg command='cloudtrail lookup-events --lookup-attributes AttributeKey=ReadOnly,AttributeValue=false --start-time START --end-time END --max-results 50'
```

START/END are replaced with verified ISO timestamps. This is one page, not an
audit. `NextToken` requires the same query and region. Keep bounded progress if
the host yields a long collection; retain checkpoint/token and page provenance.
Do not convert raw text twice through `jq -r | jq`; see [Output](gateway-and-output.md).

A busy account's first 500 events can cover only hours of a month-long request.
Use that sample to fingerprint tools/actors, not to declare no ClickOps. Report
oldest/newest sampled EventTime. Then query high-signal EventNames over the actual
window: network ingress/revokes, IAM users/policies/keys, bucket policy/public
access, resource creates/modifications. ConsoleLogin is authentication evidence,
not by itself an infrastructure mutation. Do not exceed retained/available
history and call it a complete audit.

## Attribution and challenge pass

- Terraform/CloudFormation/Pulumi fingerprints suggest clients; `aws-cli` can be
  human or CI. Identify role/session, event, resource, source and known pipelines.
  Ask about unknown automation roles after gathering evidence, not for data the
  tools already provide. IP address or a userAgent alone does not prove a human.
- AWS service/controller churn is not the team's IaC coverage. Separate it in
  counts. Tagging alone is not provisioning/configuration ownership.
- Group writes by resource and actual fields/operations. Terraform creating infra
  and Argo deploying apps can be healthy separation. Multiple tools are not a
  conflict until timelines show overlapping or reverted intent.
- K8s correlation is optional and bounded to connected in-scope clusters. Nearby
  timestamps suggest a hypothesis, not causation. Missing events cannot establish
  manual kubectl usage; use available audit/owner evidence.

Challenge each important finding: does its conclusion follow from its evidence,
or just from aggregate counts? Independent bounded review is useful for a large
audit when available; it is not a prerequisite for a narrow lookup. Give reviewers
account/region/window and evidence, not your desired verdict. Stop review loops
at an agreed budget and label unresolved conclusions; never inflate confidence.

## Deliverable

A full audit includes scope/sample completeness; tool/resource coverage; manual
actors and risky events; overlapping-resource timelines; optional K8s evidence;
per-finding impact/remedy/uncertainty; and a repeatable ResourceName timeline query.
Retain event IDs/times and concrete parameters, redacting sensitive data.

For legacy full-report workflows preserve `change-audit/scores/ops-posture.json`
and timestamped `change-audit/reports/report-<timestamp>.md` when those files are
consumed. If a 0–100 posture score is requested, explain the judgment and evidence
coverage; it is not an objective percentage of infrastructure automated. A score
without sufficient access should be withheld rather than guessed. Keep JSON and
report consistent; don't invent a rigid formula from event counts.

Publish/version only when the requested deliverable calls for it, using
[Artifacts and UI](artifacts-and-ui.md). Return the durable per-version URL, not
a session-scoped latest link. No findings is not no risk when coverage is partial.
