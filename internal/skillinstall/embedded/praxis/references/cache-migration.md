# Redis / Valkey migration

First decide whether data must move. Rebuildable cache with no cross-service
hot-key dependency can cold-start and warm up. Sessions, tokens, counters,
deduplication and other authoritative data generally need a copy/mirror with
defined consistency. Do not force RIOT on a rebuildable cache, or discard
sessions because the service is called a cache.

Record source/target, cluster/shard topology, logical databases, key patterns and
exclusions, TTL requirements, AUTH/TLS, size/headroom, downtime tolerance and the
host that reaches **both** endpoints. Local laptop reachability is not implied
by gateway access. Propose any network changes separately. If a relay/export is
needed, treat exported keys as sensitive data with a retention policy.

Use [Cloud operations](cloud-operations.md) for inventory and
[Kubernetes](kubernetes-operations.md) for scoped workload access. Infra creation/
adoption, secrets and app reference rewiring belong to Raptor and the corresponding
Praxis migration reference; they do not copy cache keys.

## Plan and execution

Preserved [spec](../scripts/cache/spec.example.json),
[plan generator](../scripts/cache/plan.py), and [monitor](../scripts/cache/monitor.py)
are narrow legacy helpers, **not a production migration gate**.

`plan.py --spec SPEC` prints draft RIOT commands only. Confirm `riot replicate
--help` and supported version, source/target cluster flags, databases, filtering,
TTL and live-change semantics. The draft does not implement all spec fields
(notably database/exclusion/TTL policy) and reads top-level `run_from`, while the
spec uses `reachability.run_from`. Reconcile these explicitly. Never execute its
stdout wholesale. AUTH substituted into a URI becomes process-argument material;
use the engine's secure input mechanism, not a token-bearing generated URI.

Keep source data read-only: no FLUSH/DEL/MIGRATE/CONFIG SET or replica-reparenting
as a shortcut. Target RESTORE/REPLACE overwrites values: idempotency does not make
that harmless. Run a non-prod pilot, show the write scope, then start the agreed
snapshot/live strategy on the approved reachability host. Record job/process
identity; client disconnect does not establish whether a mirror is still running.

## What verification proves

The bundled `monitor.py` checks DBSIZE and sampled **type/size** fingerprints.
It does **not** compare values, enforce TTL tolerance, measure replication lag,
aggregate all shards, or cover arbitrary logical databases. Its legacy SCAN
parsing/empty-sample behavior must not be used as proof of fidelity. Do not use
`VERIFY CLEAN` or its exit status as authorization to cut over.

For the actual gate use a verified RIOT comparison or an engine-aware verifier
covering the agreed keys, values/types, TTL tolerance, every shard/database and
exclusions. Collect actual copy/CDC lag separately. Report checked/expected keys,
sample size and skipped/unreadable areas. Equal DBSIZE can hide different keys;
equal type/length can hide different values; two failed reads can look equal.
An empty source requires positive proof it is empty, not an empty failed scan.

For live cutover freeze/drain writers (or document the explicitly accepted loss/
consistency window), confirm final parity and lag, check consuming-service access,
repoint, and stop the mirror in the reviewed order. Verify application sessions
and counters, not just connectivity. Retain the source per the agreed rollback
window; reverse replication/reconciliation is needed if target writes must survive
a rollback. An unattended monitor requires an explicitly requested schedule.
