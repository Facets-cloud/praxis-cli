# Cloud waste assessment

Report potential savings; do not stop/delete/rightsize resources as part of an
audit. Use [Cloud operations](cloud-operations.md), choose accounts/regions and
a representative window (14 days is a starting point, not universal proof).
List integrations first; syncing account links is not automatic preflight.

| Candidate | Evidence and important false positive |
|---|---|
| AWS available EBS / GCP disk with no users / Azure Unattached disk | retained backup/recovery purpose, owner, attachment history |
| AWS unassociated EIP | use absent AssociationId, not absent InstanceId (NAT/NLB/ENI may own it) |
| GCP RESERVED / Azure IP without ipConfiguration | reserved failover or pending deployment |
| Old/orphan snapshots | retention policy and recovery dependencies; provisioned source size is not billed snapshot bytes |
| Idle RDS/Aurora/Cloud SQL | complete connection/utilization window; DR/read replicas/health checks |
| Low-CPU VM | peak, network, memory, batch/seasonal demand; CPU alone is insufficient |
| Stopped/deallocated VM | compute vs still-billed disks/IPs; don't count compute savings already realized |
| LB with no targets/traffic | availability incident vs abandoned LB; correct ALB/NLB metric and dimension |
| Low-throughput NAT | routing and standby dependency, not just bytes |
| gp2 -> gp3 | optimization, not waste; performance requirements and change plan |

Query configuration first, then service-appropriate metrics. For AWS use
`cloudwatch get-metric-statistics` with explicit dimensions, UTC start/end and
period/statistic; measure peaks as well as averages. Empty datapoints are missing
evidence, not zero use. Resource age shorter than the window is insufficient
history. State partial regions, pagination and credential failures as unscanned.

Price from current provider pricing or authorized billing actuals for that
region/tier; quote an estimated range with assumptions. Inventory does not return
prices. Commitments, discounts, replicas, provisioned IOPS, data processing and
shared dependencies can change realizable savings. Do not reuse old USD numbers
as current or sum overlapping candidates twice.

Rank findings by confidence and potential value. Each needs resource ID, account/
region, evidence window, reason flagged, owner/retention questions and estimated
savings basis. Label high confidence **“review candidates”**, not “safe to delete”.
Separate cost evidence from authorization to remove. A clean result is valid
only for what was actually scanned. Use [Artifacts](artifacts-and-ui.md) if a
published report is requested; recurring audits use [Duties](duties.md) only when
the user requests a schedule.
