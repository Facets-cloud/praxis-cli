#!/usr/bin/env python3
"""Turn a db-migrate spec (JSON) into prerequisite changes + migration commands
+ a cutover runbook. Generates text only - runs NOTHING. Source data is treated
read-only; the prereq block is flagged because it contains CHANGES the user must
apply deliberately. Auth comes from env vars referenced by name in the spec.

Usage:  python3 plan.py --spec spec.json
"""

import argparse
import json
import sys


def prereqs(src):
    eng = src["engine"]
    if "postgres" in eng:
        return [
            "-- [USER RUNS — these are CHANGES to the source] Postgres CDC prerequisites:",
            "--  1) RDS parameter group: set rds.logical_replication = 1  (requires reboot)",
            "--  2) confirm: SHOW wal_level;  -- must be 'logical'",
            "--  3) replication role:",
            "CREATE ROLE migrator WITH LOGIN REPLICATION PASSWORD '<set>';",
            "GRANT SELECT ON ALL TABLES IN SCHEMA public TO migrator;",
            "ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO migrator;",
            "--  4) headroom: max_replication_slots / max_wal_senders >= concurrent jobs",
            "--  5) every extension in use must EXIST on the target (Cloud SQL/AlloyDB supported list).",
        ]
    return [
        "-- [USER RUNS — these are CHANGES to the source] MySQL CDC prerequisites:",
        "--  1) binlog_format = ROW ; binlog_row_image = FULL",
        "--  2) binlog retention >= migration window  (call mysql.rds_set_configuration on RDS)",
        "CREATE USER 'migrator'@'%' IDENTIFIED BY '<set>';",
        "GRANT SELECT, REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO 'migrator'@'%';",
    ]


def gcp_dms(spec):
    s = spec["source"]
    eng = "POSTGRESQL" if "postgres" in s["engine"] else "MYSQL"
    return [
        "# GCP Database Migration Service — full load + CDC, then promote.",
        "# 1) Source connection profile (RDS/Aurora):",
        f"gcloud database-migration connection-profiles create {eng.lower()} src-{spec['name']} \\",
        f"    --region=<region> --{eng.lower()} \\",
        f"    --host={s['host']} --port={s['port']} --username={s['user']} \\",
        '    --password="$SRC_DB_PASS"   # from env; do not inline the secret',
        "",
        "# 2) Migration job (CONTINUOUS = full dump + CDC):",
        f"gcloud database-migration migration-jobs create {spec['name']} \\",
        "    --region=<region> --type=CONTINUOUS \\",
        f"    --source=src-{spec['name']} --destination=<target-cloudsql/alloydb-profile> \\",
        "    --dump-flags=<engine-specific>",
        f"gcloud database-migration migration-jobs start {spec['name']} --region=<region>",
        "",
        "# 3) Watch CDC lag; when ~0 and verify.py is clean -> Step 6 cutover (promote).",
        f"gcloud database-migration migration-jobs describe {spec['name']} --region=<region>",
        "# Confirm flag names with: gcloud database-migration migration-jobs create --help",
    ]


def dump_restore(spec):
    s, t = spec["source"], spec["target"]
    db = (s.get("databases") or ["db"])[0]
    if "postgres" in s["engine"]:
        return [
            "# One-shot dump + restore (maintenance window). Source read-only (pg_dump).",
            f'PGPASSWORD="$SRC_DB_PASS" pg_dump -h {s["host"]} -p {s["port"]} -U {s["user"]} \\',
            f"    -Fc --no-owner --no-privileges -d {db} -f {db}.dump",
            f'PGPASSWORD="$DST_DB_PASS" pg_restore -h {t["host"]} -p {t["port"]} -U {t["user"]} \\',
            f"    --no-owner --no-privileges -d {db} {db}.dump",
        ]
    return [
        "# One-shot dump + restore (maintenance window). Source read-only (mysqldump).",
        f'MYSQL_PWD="$SRC_DB_PASS" mysqldump -h {s["host"]} -P {s["port"]} -u {s["user"]} \\',
        f"    --single-transaction --set-gtid-purged=OFF --routines --triggers {db} > {db}.sql",
        f'MYSQL_PWD="$DST_DB_PASS" mysql -h {t["host"]} -P {t["port"]} -u {t["user"]} {db} < {db}.sql',
    ]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--spec", required=True)
    args = ap.parse_args()
    with open(args.spec) as f:
        spec = json.load(f)
    s, t = spec["source"], spec["target"]
    strat = spec.get("strategy", "gcp-dms")

    print("=" * 72)
    print(
        f"db-migrate plan: {spec.get('name')}   {s['engine']} -> {t['kind']}   strategy={strat}"
    )
    print("=" * 72)
    print(
        f"# Run FROM: {spec.get('reachability', {}).get('run_from', '<host reaching BOTH endpoints>')}"
    )
    print(
        f"# Source DATA is READ-ONLY. Secrets via env: {s.get('auth_env')} / {t.get('auth_env')}."
    )
    print(
        f"# Downtime tolerance: {spec.get('downtime_tolerance', '<confirm with user>')}\n"
    )

    print("## Step 1a — PREREQUISITES (user applies these; they are CHANGES)")
    for line in prereqs(s):
        print("  " + line)
    print(
        "  -- Network: ensure the source RDS SG admits the migration connection (VPN/Interconnect/"
    )
    print(
        "  --          peering, or RDS publicly-accessible + IP allowlist). Same reachability gate.\n"
    )

    print("## Step 3 — MIGRATION COMMANDS")
    body = gcp_dms(spec) if strat == "gcp-dms" else dump_restore(spec)
    if strat == "aws-dms":
        print(
            "  # aws-dms selected: create a replication instance + endpoints + task in AWS DMS"
        )
        print(
            "  # (use for heterogeneous, or when GCP DMS can't reach/support the source)."
        )
    for line in body:
        print("  " + line)

    print("\n## Step 5 — VERIFY (after load / before cutover)")
    print(f"  python3 verify.py --spec {args.spec} --checksum")

    print("\n## Step 6 — CUTOVER")
    print("  1) stop writers on source  2) drain CDC to lag~0  3) final verify.py")
    print(
        "  4) RESET sequences/AUTO_INCREMENT on target (CDC does NOT advance them — #1 footgun)"
    )
    if "postgres" in s["engine"]:
        print(
            "     PG:  SELECT setval(pg_get_serial_sequence('t','id'), (SELECT max(id) FROM t));"
        )
    else:
        print("     MySQL: ALTER TABLE t AUTO_INCREMENT = <max(id)+1>;")
    print(
        "  5) repoint apps  6) promote target (GCP DMS: promote = stop replication, standalone)"
    )
    print("  Keep the source read-only until the target is confirmed healthy.")


if __name__ == "__main__":
    sys.exit(main())
