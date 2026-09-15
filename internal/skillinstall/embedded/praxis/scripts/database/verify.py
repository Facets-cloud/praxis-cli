#!/usr/bin/env python3
"""db-migrate verification: per-table ROW-COUNT (+ optional CHECKSUM) parity
between source and target. READ-ONLY on both ends - only issues SELECT / SHOW /
CHECKSUM TABLE via the engine CLI (psql / mysql). Auth from env vars named in the
spec (PGPASSWORD / MYSQL_PWD), never on the command line or in logs.

Usage:
  python3 verify.py --spec spec.json                  # row counts, all in-scope tables
  python3 verify.py --spec spec.json --checksum       # + per-table content checksum
Requires psql (postgres) or mysql (mysql) on PATH + network reach to both ends.
"""

import argparse
import json
import os
import subprocess
import sys

READONLY_PREFIXES = ("SELECT", "SHOW", "CHECKSUM TABLE", "WITH")


def is_pg(engine):
    return "postgres" in engine


def q(node, db, sql, engine):
    if not sql.lstrip().upper().startswith(READONLY_PREFIXES):
        raise SystemExit(f"refusing non-readonly SQL: {sql[:40]}")
    env = dict(os.environ)
    tok = os.environ.get(node.get("auth_env", ""), "")
    if is_pg(engine):
        if tok:
            env["PGPASSWORD"] = tok
        cmd = [
            "psql",
            "-h",
            str(node["host"]),
            "-p",
            str(node["port"]),
            "-U",
            node["user"],
            "-d",
            db,
            "-tAqc",
            sql,
        ]
    else:
        if tok:
            env["MYSQL_PWD"] = tok
        cmd = [
            "mysql",
            "-h",
            str(node["host"]),
            "-P",
            str(node["port"]),
            "-u",
            node["user"],
            "-D",
            db,
            "-N",
            "-B",
            "-e",
            sql,
        ]
    out = subprocess.run(cmd, capture_output=True, text=True, env=env, timeout=120)
    if out.returncode != 0:
        return f"ERR:{out.stderr.strip().splitlines()[-1][:80] if out.stderr.strip() else 'failed'}"
    return out.stdout.strip()


def list_tables(node, db, schema, engine):
    if is_pg(engine):
        sql = (
            f"SELECT table_name FROM information_schema.tables WHERE table_schema='{schema}' "
            "AND table_type='BASE TABLE' ORDER BY table_name"
        )
    else:
        sql = (
            f"SELECT table_name FROM information_schema.tables WHERE table_schema='{db}' "
            "AND table_type='BASE TABLE' ORDER BY table_name"
        )
    out = q(node, db, sql, engine)
    return [] if out.startswith("ERR") else [t for t in out.splitlines() if t]


def pg_pk(node, db, schema, table):
    sql = (
        f"SELECT a.attname FROM pg_index i JOIN pg_attribute a "
        f"ON a.attrelid=i.indrelid AND a.attnum=ANY(i.indkey) "
        f"WHERE i.indrelid='{schema}.{table}'::regclass AND i.indisprimary "
        "ORDER BY array_position(i.indkey, a.attnum)"
    )
    out = q(node, db, sql, "postgres")
    return [] if out.startswith("ERR") else [c for c in out.splitlines() if c]


def row_count(node, db, schema, table, engine):
    rel = f"{schema}.{table}" if is_pg(engine) else f"`{table}`"
    out = q(node, db, f"SELECT count(*) FROM {rel}", engine)
    try:
        return int(out.split()[-1])
    except Exception:
        return -1


def checksum(node, db, schema, table, engine, order_cols):
    if is_pg(engine):
        if not order_cols:
            return "no-pk(skip)"
        order = ",".join(order_cols)
        sql = f"SELECT md5(string_agg(t::text, '' ORDER BY {order})) FROM {schema}.{table} t"
        out = q(node, db, sql, engine)
        return out.split()[-1] if not out.startswith("ERR") else out
    out = q(node, db, f"CHECKSUM TABLE `{table}`", engine)  # mysql built-in
    return out.split()[-1] if not out.startswith("ERR") else out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--spec", required=True)
    ap.add_argument("--checksum", action="store_true")
    args = ap.parse_args()
    with open(args.spec) as f:
        spec = json.load(f)
    s, t = spec["source"], spec["target"]
    engine = s["engine"]
    db = (s.get("databases") or ["postgres"])[0]
    schema = (spec.get("scope", {}).get("schemas") or ["public"])[0]

    tables = spec.get("scope", {}).get("tables_include") or list_tables(
        s, db, schema, engine
    )
    excl = set(spec.get("scope", {}).get("tables_exclude") or [])
    tables = [x for x in tables if x not in excl]
    if not tables:
        print("No tables resolved (check connectivity/scope).")
        return 1

    print(
        f"db-migrate verify: {spec.get('name')}  ({engine})  db={db} schema={schema}  [READ-ONLY]"
    )
    print(f"{'table':40} {'src_rows':>12} {'dst_rows':>12} {'delta':>10}  checksum")
    print("-" * 90)
    mism = 0
    for tbl in tables:
        sc, dc = (
            row_count(s, db, schema, tbl, engine),
            row_count(t, db, schema, tbl, engine),
        )
        delta = (dc - sc) if (sc >= 0 and dc >= 0) else "ERR"
        cs = ""
        if args.checksum:
            order = pg_pk(s, db, schema, tbl) if is_pg(engine) else None
            scs = checksum(s, db, schema, tbl, engine, order)
            dcs = checksum(t, db, schema, tbl, engine, order)
            cs = (
                "MATCH"
                if (scs == dcs and not str(scs).startswith(("ERR", "no-pk")))
                else (str(scs) if str(scs).startswith("no-pk") else "MISMATCH")
            )
        bad = (delta != 0) or (cs == "MISMATCH")
        if bad:
            mism += 1
        print(f"{tbl[:40]:40} {sc:>12} {dc:>12} {delta!s:>10}  {cs}")
    print("-" * 90)
    print(
        f"tables={len(tables)}  mismatched={mism}  => {'CLEAN' if mism == 0 else 'DIFFERENCES FOUND'}"
    )
    print(
        "Note: row-count match is necessary, not sufficient — run with --checksum before cutover, "
        "and remember to RESET sequences/AUTO_INCREMENT on the target post-cutover."
    )
    return 0 if mism == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
