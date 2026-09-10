# Relational data migration

This moves **data**, not Terraform ownership. For bringing an existing database
under Facets management use the **raptor skill's imports route first**, including
plan-first zero-change adoption. Importing infra does not copy rows, create all
application users or prove credentials work.

## Choose the data-plane move

Record source/target engine and version, databases/schemas/tables, excluded data,
extensions/large objects, size, downtime tolerance, TLS and a host/path reaching
both endpoints. Discover cloud config through [Cloud operations](cloud-operations.md).
Cloud discovery access is not database/replication access.

- Compatible engines with a tight window: evaluate managed full-load + CDC
  (e.g. GCP DMS for RDS/Aurora -> Cloud SQL/AlloyDB) against current supported
  versions/features. Do not assume every engine/target supports that service.
- Small scope with a maintenance window: dump/restore may be simpler.
- Heterogeneous/large migrations need explicit conversion/catch-up design;
  selecting `aws-dms` in a helper is not an implemented migration engine.

Source **data** remains read-only during migration. Enabling logical replication/
binlog, reboots/parameter groups, replication users/grants and network rules are
source changes: propose them with effects; apply only under separate explicit
scope. Don't silently open public access to overcome reachability.

Postgres CDC needs appropriate logical WAL/slots/senders and replication access;
MySQL needs row-based binlog/full row image and retention covering catch-up.
Confirm source extensions/types exist on target. Inspect service-specific
prerequisites rather than applying a universal parameter recipe.

## Preserved helpers and their limits

[Example spec](../scripts/database/spec.example.json),
[plan generator](../scripts/database/plan.py),
[verification helper](../scripts/database/verify.py).

The generator **prints a draft only**. Review installed migration-tool help,
quoting, scope, authentication and target objects before any execution; never
pipe its output into a shell. It contains placeholder DMS flags, first-database
dump examples and fixed auth variable names. Its `aws-dms` branch also prints a
dump recipe: that is not an AWS DMS implementation. Password expansion in generated
CLI arguments can expose values in process listings; replace with a supported
secure input method. Dump files contain data: protect/encrypt and expire them.

The verifier performs row counts plus optional checksums via psql/mysql. It only
uses the **first database and first schema** and applies table include/exclude
lists. Run a separately scoped spec for each pair; reconcile the complete table
inventory. Validate trusted identifiers; this is not an arbitrary SQL sandbox.
Configure/verify TLS for the engine client separately—the spec's `tls` field does
not itself establish an enforced encrypted connection in this helper.

Postgres ordered aggregate checksums require a stable PK and can be expensive
on large tables; keyless tables emit `no-pk(skip)`. Counts alone are not content
parity. Empty-table/NULL and engine checksum compatibility need explicit handling
and independent evidence. A `CLEAN` exit does not prove all schemas, tables,
sequences, permissions or runtime behaviors match. Prefer engine-supported,
bounded checks where this helper cannot establish the needed invariant.

## Execute and cut over

After the reviewed runbook is authorized: establish replication/load, retain job
IDs, monitor load failures and actual CDC lag, then verify target schema and data.
Reaching lag near zero on a busy source is not a consistent final snapshot.

```text
Initial load + catch-up -> freeze/drain source writers -> drain CDC
  -> final scoped parity -> target sequences/identity & grants
  -> promote/repoint in engine-required order -> application verification
```

Promotion/repoint ordering depends on the migration service; follow its exact
contract. Ensure sequences/AUTO_INCREMENT will not collide on the first new
insert, including empty tables. Check users, grants, networking and connection
strings separately. Raptor expression discovery wires Facets-managed consumers.
Keep the source protected during the rollback window. Once the target accepts
writes, repointing back can lose them: define reconciliation before cutover, not
after. Never describe Terraform rollback as data rollback.
