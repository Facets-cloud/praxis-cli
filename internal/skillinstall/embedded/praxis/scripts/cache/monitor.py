#!/usr/bin/env python3
"""Monitor a cache migration: key-count parity (loop) and sampled key fidelity.

READ-ONLY by construction — only a whitelist of read commands is ever issued to
either endpoint. Auth is taken from the env vars named in the spec (via
REDISCLI_AUTH), so tokens never appear on the command line or in logs.

Usage:
  python3 monitor.py --spec spec.json --interval 30          # parity loop
  python3 monitor.py --spec spec.json --verify --sample 200  # sampled fidelity
Requires: redis-cli on PATH and network reachability to both endpoints.
"""

import argparse
import json
import os
import subprocess
import sys
import time

READONLY = {
    "DBSIZE",
    "SCAN",
    "TYPE",
    "PTTL",
    "TTL",
    "STRLEN",
    "LLEN",
    "SCARD",
    "HLEN",
    "ZCARD",
    "GET",
    "RANDOMKEY",
    "INFO",
    "PING",
}


def rcli(node, *cmd):
    if cmd[0].upper() not in READONLY:
        raise SystemExit(f"refusing non-readonly command: {cmd[0]}")
    args = ["redis-cli", "-h", str(node["host"]), "-p", str(node["port"]), "--no-raw"]
    if node.get("tls"):
        args.append("--tls")
    if node.get("user"):
        args += ["--user", node["user"]]
    args += [str(c) for c in cmd]
    env = dict(os.environ)
    tok = os.environ.get(node.get("auth_env", ""), "")
    if tok:
        env["REDISCLI_AUTH"] = tok
    out = subprocess.run(args, capture_output=True, text=True, env=env, timeout=30)
    if out.returncode != 0:
        return f"ERR:{out.stderr.strip()[:80]}"
    return out.stdout.strip()


def dbsize(node):
    v = rcli(node, "DBSIZE")
    try:
        return int(v.split()[-1])
    except Exception:
        return -1


def fingerprint(node, key):
    """Cheap type+size+ttl fingerprint — no value bytes transferred."""
    t = rcli(node, "TYPE", key).split()[-1]
    ttl = rcli(node, "PTTL", key).split()[-1]
    sizecmd = {
        "string": "STRLEN",
        "list": "LLEN",
        "set": "SCARD",
        "hash": "HLEN",
        "zset": "ZCARD",
    }.get(t)
    size = rcli(node, sizecmd, key).split()[-1] if sizecmd else "0"
    return (t, size, ttl)


def sample_keys(node, pattern, n):
    keys, cursor = [], "0"
    while len(keys) < n:
        out = rcli(node, "SCAN", cursor, "MATCH", pattern, "COUNT", 500)
        lines = [ln.strip().strip('"') for ln in out.splitlines() if ln.strip()]
        if not lines:
            break
        cursor = lines[0]
        keys += [k for k in lines[1:] if k]
        if cursor == "0":
            break
    return keys[:n]


def parity(spec):
    s, d = dbsize(spec["source"]), dbsize(spec["target"])
    pct = (100.0 * d / s) if s > 0 else 0.0
    print(
        f"  source DBSIZE={s}  target DBSIZE={d}  parity={pct:.1f}%"
        + (
            "  [cluster: per-node, use --cluster aggregation]"
            if spec["source"].get("cluster_mode") == "enabled"
            else ""
        )
    )
    return s, d


def verify(spec, sample):
    pats = spec.get("scope", {}).get("key_patterns", ["*"]) or ["*"]
    miss, typ, sz, ok = [], [], [], 0
    checked = 0
    for pat in pats:
        for k in sample_keys(spec["source"], pat, max(1, sample // len(pats))):
            checked += 1
            sf = fingerprint(spec["source"], k)
            df = fingerprint(spec["target"], k)
            if df[0] in ("none", "ERR") or df == ("none", "0", "-2"):
                miss.append(k)
            elif sf[0] != df[0]:
                typ.append((k, sf[0], df[0]))
            elif sf[1] != df[1]:
                sz.append((k, sf[1], df[1]))
            else:
                ok += 1
    print(
        f"  sampled={checked}  ok={ok}  missing={len(miss)}  "
        f"type_mismatch={len(typ)}  size_mismatch={len(sz)}"
    )
    for k in miss[:10]:
        print(f"    MISSING: {k}")
    for k, a, b in typ[:10]:
        print(f"    TYPE  {k}: src={a} dst={b}")
    for k, a, b in sz[:10]:
        print(f"    SIZE  {k}: src={a} dst={b}")
    return not (miss or typ or sz)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--spec", required=True)
    ap.add_argument(
        "--interval", type=int, default=0, help="parity loop seconds (0 = once)"
    )
    ap.add_argument("--verify", action="store_true", help="sampled key fidelity check")
    ap.add_argument("--sample", type=int, default=200)
    args = ap.parse_args()
    with open(args.spec) as f:
        spec = json.load(f)

    if args.verify:
        print(f"[verify] {spec.get('name')}")
        clean = verify(spec, args.sample)
        print("  => VERIFY", "CLEAN" if clean else "FOUND DIFFERENCES")
        return 0 if clean else 1

    while True:
        print(f"[parity] {time.strftime('%H:%M:%S')} {spec.get('name')}")
        s, d = parity(spec)
        if args.interval <= 0:
            break
        time.sleep(args.interval)
    return 0


if __name__ == "__main__":
    sys.exit(main())
