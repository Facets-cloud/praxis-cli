#!/usr/bin/env python3
"""
Replicate a workload's secrets from AWS Secrets Manager -> GCP Secret Manager.

Run by an operator whose shell has:
  * AWS credentials with `secretsmanager:GetSecretValue` on the in-scope secrets
    (a read-only discovery/federation role does NOT have this).
  * `gcloud` authenticated as a principal with roles/secretmanager.secretVersionAdder
    on the target GCP secrets. The secrets themselves are created ahead of time
    by an IaC `secret_manager` module -- this script NEVER creates secrets,
    it only adds a value version to a secret that already exists.

For each row in the mapping file, it reads the AWS secret's CURRENT value
(a JSON object) and writes it VERBATIM as a new version of the corresponding
GCP Secret Manager secret. ESO's `dataFrom.extract` then unpacks the same JSON
into the `<workload>-secret` Kubernetes Secret -- identical contract to the source.

SAFETY
  * Secret material is held in memory only -- never written to disk, never logged.
  * The value is piped to `gcloud` via stdin (never passed as an argv).
  * Idempotent: re-running adds a new version; --skip-unchanged avoids version
    churn when the value already matches.
  * --dry-run reads + validates but writes nothing.

No third-party Python packages required -- shells out to the `aws` and `gcloud`
CLIs you already have.
"""

import argparse
import hashlib
import json
import subprocess
import sys


def run(cmd, input_bytes=None):
    """Run a command; return (returncode, stdout_bytes, stderr_text)."""
    p = subprocess.run(
        cmd, input=input_bytes, stdout=subprocess.PIPE, stderr=subprocess.PIPE
    )
    return p.returncode, p.stdout, p.stderr.decode("utf-8", "replace")


def aws_base(profile, region):
    cmd = ["aws"]
    if profile:
        cmd += ["--profile", profile]
    if region:
        cmd += ["--region", region]
    return cmd


def aws_get_secret_value(profile, region, name):
    """Return (kind, value) where kind is 'string' | 'binary' | None. Raw value stays in-process."""
    rc, out, err = run(
        aws_base(profile, region)
        + [
            "secretsmanager",
            "get-secret-value",
            "--secret-id",
            name,
            "--output",
            "json",
        ]
    )
    if rc != 0:
        raise RuntimeError(err.strip() or f"aws get-secret-value failed for {name}")
    obj = json.loads(out)
    if obj.get("SecretString") is not None:
        return "string", obj["SecretString"]
    if obj.get("SecretBinary") is not None:
        return "binary", obj["SecretBinary"]
    return None, None


def gcp_secret_exists(project, sid):
    rc, _, _ = run(
        [
            "gcloud",
            "secrets",
            "describe",
            sid,
            "--project",
            project,
            "--format=value(name)",
        ]
    )
    return rc == 0


def gcp_add_version(project, sid, value_str):
    rc, _, err = run(
        [
            "gcloud",
            "secrets",
            "versions",
            "add",
            sid,
            "--project",
            project,
            "--data-file=-",
        ],
        input_bytes=value_str.encode("utf-8"),
    )
    if rc != 0:
        raise RuntimeError(err.strip() or f"gcloud versions add failed for {sid}")


def gcp_access_latest(project, sid):
    """Return the latest version value as a string, or None if no versions yet."""
    rc, out, err = run(
        [
            "gcloud",
            "secrets",
            "versions",
            "access",
            "latest",
            "--project",
            project,
            "--secret",
            sid,
        ]
    )
    if rc != 0:
        return None
    return out.decode("utf-8", "replace")


def digest(s):
    return hashlib.sha256(s.encode("utf-8")).hexdigest()[:12]


def classify_value(kind, value):
    """Categorize a secret value for auto-replicability. Returns (category, keys_or_None, note).

    'ok'      - flat JSON object of string values -> safe to auto-copy (ESO-extractable)
    'nonflat' - JSON object with non-string values -> auto-copy works but ESO extraction is
                shape-sensitive -> verify in a non-prod env
    'manual'  - binary / non-JSON plaintext / top-level JSON scalar/array -> NOT
                dataFrom.extract-able -> copy by hand with the right k8s wiring
    """
    if kind == "binary":
        return ("manual", None, "binary (SecretBinary) — not env/JSON-extractable")
    if kind != "string":
        return ("manual", None, f"empty/unknown payload ({kind})")
    try:
        obj = json.loads(value)
    except Exception:
        return ("manual", None, "non-JSON plaintext — not dataFrom.extract-able")
    if not isinstance(obj, dict):
        return ("manual", None, f"top-level JSON {type(obj).__name__}, not an object")
    nonstr = sorted(k for k, v in obj.items() if not isinstance(v, str))
    if nonstr:
        return (
            "nonflat",
            sorted(obj.keys()),
            "non-string values: " + ", ".join(nonstr[:6]),
        )
    return ("ok", sorted(obj.keys()), "")


def load_mapping(path):
    rows = []
    with open(path) as f:
        for raw in f:
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            parts = [p.strip() for p in line.split(",")]
            if len(parts) < 2 or parts[0].lower() == "aws_secret_name":
                continue
            rows.append((parts[0], parts[1]))
    return rows


def main():
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    ap.add_argument(
        "--mapping", required=True, help="CSV: aws_secret_name,gcp_secret_id"
    )
    ap.add_argument("--gcp-project", required=True, help="Target GCP project id")
    ap.add_argument(
        "--aws-profile", default=None, help="AWS profile (else env/default)"
    )
    ap.add_argument(
        "--aws-region",
        default=None,
        help="AWS region for the source secrets (else your aws config/env default)",
    )
    ap.add_argument(
        "--skip-unchanged",
        action="store_true",
        default=True,
        help="Skip adding a version if GCP latest already matches (default on)",
    )
    ap.add_argument(
        "--force",
        dest="skip_unchanged",
        action="store_false",
        help="Always add a new version even if unchanged",
    )
    ap.add_argument(
        "--allow-plaintext",
        action="store_true",
        help="Allow non-JSON (plaintext) values; default is to flag them NEEDS-MANUAL "
        "(ESO dataFrom.extract needs a JSON object)",
    )
    ap.add_argument(
        "--dry-run", action="store_true", help="Read+classify, write nothing"
    )
    args = ap.parse_args()

    rows = load_mapping(args.mapping)
    if not rows:
        print("No mapping rows found.", file=sys.stderr)
        sys.exit(2)

    print(f"{'AWS SECRET':38} {'GCP SECRET ID':32} {'KEYS':>4}  ACTION / STATUS")
    print("-" * 100)

    results = []
    for aws_name, gcp_id in rows:
        try:
            kind, value = aws_get_secret_value(
                args.aws_profile, args.aws_region, aws_name
            )
        except Exception as e:
            results.append((aws_name, gcp_id, "ERROR", str(e)))
            print(f"{aws_name:38} {gcp_id:32} {'-':>4}  ERROR: {e}")
            continue

        cat, keys, note = classify_value(kind, value)
        nkeys = str(len(keys)) if keys else "-"
        if cat == "manual" and not (kind == "string" and args.allow_plaintext):
            results.append((aws_name, gcp_id, "NEEDS-MANUAL", note))
            print(f"{aws_name:38} {gcp_id:32} {nkeys:>4}  NEEDS-MANUAL — {note}")
            continue
        warn = (
            "  [nonflat values — verify extraction in a non-prod env]"
            if cat == "nonflat"
            else ""
        )
        new_h = digest(value)

        if not gcp_secret_exists(args.gcp_project, gcp_id):
            msg = "GCP secret does not exist — create it via your secret_manager IaC module first"
            results.append((aws_name, gcp_id, "ERROR", msg))
            print(f"{aws_name:38} {gcp_id:32} {nkeys:>4}  ERROR: {msg}")
            continue

        if args.dry_run:
            results.append((aws_name, gcp_id, "DRYRUN", "exists"))
            print(
                f"{aws_name:38} {gcp_id:32} {nkeys:>4}  DRY-RUN -> add-version (exists, sha {new_h}){warn}"
            )
            continue

        try:
            if args.skip_unchanged:
                cur = gcp_access_latest(args.gcp_project, gcp_id)
                if cur is not None and digest(cur) == new_h:
                    results.append((aws_name, gcp_id, "NO-CHANGE", f"sha {new_h}"))
                    print(
                        f"{aws_name:38} {gcp_id:32} {nkeys:>4}  NO-CHANGE (sha {new_h}){warn}"
                    )
                    continue

            gcp_add_version(args.gcp_project, gcp_id, value)

            # Verify: read back, compare hash. Never print values.
            back = gcp_access_latest(args.gcp_project, gcp_id)
            ok = back is not None and digest(back) == new_h
            status = "OK" if ok else "VERIFY-FAIL"
            results.append((aws_name, gcp_id, status, f"sha {new_h}"))
            print(f"{aws_name:38} {gcp_id:32} {nkeys:>4}  {status} (sha {new_h}){warn}")
        except Exception as e:
            results.append((aws_name, gcp_id, "ERROR", str(e)))
            print(f"{aws_name:38} {gcp_id:32} {nkeys:>4}  ERROR: {e}")

    print("-" * 100)
    counts = {}
    for r in results:
        counts[r[2]] = counts.get(r[2], 0) + 1
    print("Summary:", ", ".join(f"{k}={v}" for k, v in sorted(counts.items())))

    manual = [r for r in results if r[2] == "NEEDS-MANUAL"]
    if manual:
        print(
            "\n*** MANUAL COPY REQUIRED — NOT auto-replicable (binary / non-JSON / scalar)."
        )
        print(
            "    Copy by hand with the correct k8s wiring (data: key mapping or a volume mount): ***"
        )
        for aws_name, gcp_id, _, note in manual:
            print(f"    - {aws_name}  ->  {gcp_id}   ({note})")

    bad = [r for r in results if r[2] in ("ERROR", "VERIFY-FAIL", "NEEDS-MANUAL")]
    sys.exit(1 if bad else 0)


if __name__ == "__main__":
    main()
