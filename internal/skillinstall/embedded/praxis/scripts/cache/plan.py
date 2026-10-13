#!/usr/bin/env python3
"""Turn a cache-migrate spec (JSON) into a RIOT replicate command + a runbook.

Generates the command only — it does NOT run anything. Source is treated as
read-only; auth tokens are passed via env vars (referenced by name in the spec),
never embedded in the spec or the printed command.

Usage:  python3 plan.py --spec spec.json
"""

import argparse
import json
import sys


def uri(node, auth_env_placeholder):
    scheme = "rediss" if node.get("tls") else "redis"
    # Password is injected from an env var at run time; we print a placeholder
    # so the token never lands in the spec or the generated command text.
    return f"{scheme}://:{auth_env_placeholder}@{node['host']}:{node['port']}"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--spec", required=True)
    args = ap.parse_args()

    with open(args.spec) as f:
        spec = json.load(f)

    src, dst, scope = spec["source"], spec["target"], spec.get("scope", {})
    mode = spec.get("mode", "snapshot")
    patterns = scope.get("key_patterns", ["*"]) or ["*"]
    src_auth_env = src.get("auth_env", "SRC_REDIS_AUTH")
    dst_auth_env = dst.get("auth_env", "DST_REDIS_AUTH")

    riot_mode = {"snapshot": "snapshot", "live": "live", "verify": "compare"}.get(
        mode, "snapshot"
    )

    # One replicate invocation per key pattern (RIOT takes a single scan match).
    cmds = []
    for pat in patterns:
        parts = [
            "riot replicate",
            f'"{uri(src, "$" + src_auth_env)}"',
            f'"{uri(dst, "$" + dst_auth_env)}"',
            f"--mode {riot_mode}",
            f"--scan-match '{pat}'",
            "--scan-count 1000",
            "--batch 50",
        ]
        if src.get("cluster_mode") == "enabled":
            parts.append(
                "--cluster"
            )  # source cluster mode; verify flag for your RIOT version
        cmds.append(" \\\n    ".join(parts))

    print("=" * 72)
    print(f"cache-migrate plan: {spec.get('name', '(unnamed)')}   mode={mode}")
    print("=" * 72)
    print("\n# READ-ONLY SOURCE. Idempotent target (RESTORE REPLACE). Re-runnable.")
    print(f"# Run FROM: {spec.get('run_from', '<host reachable to BOTH endpoints>')}")
    print("# Export tokens first (never commit them):")
    print(f"#   export {src_auth_env}=...    # source AUTH token")
    print(f"#   export {dst_auth_env}=...    # target AUTH token")
    print("# Confirm RIOT flags for your version: riot replicate --help\n")

    for i, c in enumerate(cmds, 1):
        print(f"# --- pattern {i}/{len(cmds)} ---")
        print(c)
        print()

    if mode == "verify":
        print("# verify mode: RIOT 'compare' reports key/value diffs without writing.")
    elif mode == "live":
        print("# live mode: keeps mirroring until you Ctrl-C at cutover.")
        print("# Watch parity with monitor.py; cut over when lag ~ 0.")

    exp = spec.get("expected_keys")
    if exp:
        print(
            f"\n# Sanity: expect ~{exp} keys for these patterns "
            "(compare to monitor.py output)."
        )
    print("\n# Next: review -> run (gated) -> python3 monitor.py --spec %s" % args.spec)


if __name__ == "__main__":
    sys.exit(main())
