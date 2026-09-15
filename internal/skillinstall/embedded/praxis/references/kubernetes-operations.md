# Kubernetes operations

Use deployed `k8s_cli` functions: `list_connected_clusters`, `run_k8s_cli`,
`run_helm_cli`. Select a verified integration, namespace and workload before
querying; an example named `prod` is not a safe default. Connect-EKS/GKE tools
change integrations and are not implicit diagnostics.

```bash
praxis mcp k8s_cli run_k8s_cli --json \
  --arg integration_name=CLUSTER --arg namespace=NAMESPACE \
  --arg command='logs POD -c CONTAINER --previous --tail=100 --since=15m'
```

Omit `kubectl`/`helm` prefixes inside commands. Gateway access uses org-managed
credentials and an audit trail; do not bypass a rejected operation with local
credentials. A separately authorized local runner for data migration is not
covered by this diagnostic restriction.

## Follow the first failing phase

| Symptom | Evidence to inspect |
|---|---|
| Pending | scheduling events, requests vs allocatable, affinity/taints, quotas, PVC binding |
| ImagePullBackOff | image reference, registry access, pull events; no credential dumps |
| Init failure | init container termination reason and its bounded logs |
| CrashLoopBackOff | container last termination, exit/OOM reason, previous logs, probes |
| Running but unready | readiness, endpoints, dependency connectivity, application errors |
| Service unreachable | selectors, EndpointSlices/endpoints, ready backends, ports, policy/DNS |

Read a pod's describe/events first where phase is unclear. Choose `-c` when
multiple containers exist. Use `--tail`/`--since` on logs and namespace filters;
do not default every issue to cluster-wide dumps. Events/metrics have retention
and availability limits; missing history is not proof no event occurred.

`exec` is remote execution even if a gateway permits only selected commands.
Inspect exact command effects and data exposure. Never run broad `env`, secret
decoding or application-config dumps as routine discovery. Prefer metadata/key
names and explicitly non-sensitive probes. Do not call all permitted exec
commands harmless merely because kubectl's outer verb passed validation.

## Helm and change correlation

`run_helm_cli` supports release `status`, `history`, `get values --revision N`,
`get manifest`, hooks and notes for the same selected cluster. Track exact Helm
release/revision/namespace. Sensitive values can appear in manifests and values:
filter/redact at the source. A newer failed Helm revision can differ from the
deployed revision; Raptor release completion is another distinct layer.

Check deployed manifest/help when a verb is blocked; some older examples mention
rollout operations that the gateway policy rejects. Use permitted deployment/
ReplicaSet/event/Helm reads instead of retrying unsupported verbs.

Conclude with cause -> mechanism -> symptom -> impact, distinguishing inference
from evidence. For a change-induced failure compare before/after and timeline;
for steady-state issues examine actual saturation/configuration. Diagnosis does
not authorize scale/restart/patch. Facets-managed repairs should follow the
**raptor skill** configuration/release workflow when implementation is requested.
