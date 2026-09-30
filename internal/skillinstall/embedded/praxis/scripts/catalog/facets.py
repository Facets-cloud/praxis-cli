#!/usr/bin/env python3
"""facets.py — thin reader over `raptor get ... -o json` for ig onboarding.

Runs raptor with RAPTOR_NO_UPDATE_CHECK=1 so stdout is clean JSON (without it
raptor appends an "update available" banner that breaks json.load).

Subcommands (all print JSON so the caller can build labeled AskUserQuestion options):
  projects          → [{"name","projectType","test","lastModified","description"}]
                      sorted most-recently-modified first.
  envs <project>    → [{"name","cloud","status","lastRelease","clusterState"}]
                      sorted most-recently-released first (dead/never-released last).
  services <project>→ [{"service","artifact","resourceType"}] — only deployable
                      resources with an artifact attached. A resource "has an
                      artifact attached" when spec.release.image is
                      ${blueprint.self.artifacts.X} OR spec.release.build.name is set.
                      A stderr note reports resource types that were NOT scanned.

Exit codes: 0 ok, 2 raptor/parse failure (message on stderr).
"""

import concurrent.futures as cf
import json
import os
import re
import subprocess
import sys

ARTIFACT_EXPR = re.compile(r"\$\{blueprint\.self\.artifacts\.([A-Za-z0-9_-]+)\}")
# Resource types that carry a deployable image/build. Non-deployable types
# (s3, redis, iam, helm charts, k8s_resource, ...) are not scanned for artifacts;
# cmd_services prints a stderr note of what it skipped so the gap is visible.
DEPLOYABLE_TYPES = {"service", "application"}


class RaptorError(Exception):
    """A raptor lookup failed. Raised (not die()) so worker threads unwind
    cleanly and main() reports the failure exactly once."""


def raptor_json(*args):
    """Run `raptor get <args> -o json` (banner suppressed) and parse the JSON.

    Raises RaptorError on any failure — non-zero exit (stderr surfaced), or
    empty / non-JSON output — so it's safe to call from worker threads (see
    cmd_services); main() turns the first RaptorError into a single die()."""
    env = {**os.environ, "RAPTOR_NO_UPDATE_CHECK": "1"}
    label = f"raptor get {' '.join(args)}"
    try:
        proc = subprocess.run(
            ["raptor", "get", *args, "-o", "json"],
            capture_output=True,
            text=True,
            timeout=120,
            env=env,
        )
    except Exception as e:
        raise RaptorError(f"{label}: {e}") from e
    if proc.returncode != 0:
        detail = (
            (proc.stderr or "").strip() or (proc.stdout or "").strip() or "no output"
        )
        raise RaptorError(f"{label}: exited {proc.returncode}: {detail}")
    s = proc.stdout.strip()
    if not s:
        raise RaptorError(f"{label}: empty output (is FACETS_PROFILE / auth set?)")
    try:
        return json.loads(s)
    except ValueError as e:
        raise RaptorError(f"{label}: not JSON ({e})") from e


def aslist(v, key):
    if isinstance(v, dict):
        v = v.get(key, [])
    return v if isinstance(v, list) else []


def artifact_of(cfg):
    """Return the artifact/build name a resource config attaches, or None."""
    rel = (cfg.get("spec") or {}).get("release") or {}
    img = rel.get("image")
    if isinstance(img, str):
        m = ARTIFACT_EXPR.search(img)
        if m:
            return m.group(1)
    build = rel.get("build") or {}
    name = build.get("name")
    if isinstance(name, str) and name.strip():
        return name.strip()
    return None


def cmd_projects():
    rows = []
    for p in raptor_json("projects"):
        if not isinstance(p, dict) or not p.get("name"):
            continue
        rows.append(
            {
                "name": p.get("name"),
                "projectType": p.get("projectType"),
                "test": bool(p.get("isTestProject")),
                "lastModified": p.get("lastModifiedDate"),
                "description": p.get("description") or "",
            }
        )
    rows.sort(key=lambda r: r["lastModified"] or "", reverse=True)
    print(json.dumps(rows, indent=2))


def cmd_envs(project):
    rows = []
    for e in aslist(raptor_json("environments", "-p", project), "environments"):
        if not isinstance(e, dict) or not e.get("name"):
            continue
        rows.append(
            {
                "name": e.get("name"),
                "cloud": e.get("cloud"),
                "status": e.get("lastReleaseStatus"),
                "lastRelease": e.get("lastReleaseDate"),
                "clusterState": e.get("clusterState"),
            }
        )
    rows.sort(key=lambda r: r["lastRelease"] or "", reverse=True)
    print(json.dumps(rows, indent=2))


def cmd_services(project):
    resources = aslist(raptor_json("resources", "-p", project), "resources")
    refs, skipped = [], {}
    for r in resources:
        if not isinstance(r, dict) or not r.get("name"):
            continue
        rt = r.get("resourceType")
        if rt in DEPLOYABLE_TYPES:
            refs.append((rt, r.get("name")))
        else:
            skipped[rt] = skipped.get(rt, 0) + 1

    def fetch(rt_name):
        rt, name = rt_name
        cfg = raptor_json("resources", f"{rt}/{name}", "-p", project)
        art = artifact_of(cfg) if isinstance(cfg, dict) else None
        return {"service": name, "artifact": art, "resourceType": rt} if art else None

    out = []
    with cf.ThreadPoolExecutor(max_workers=8) as ex:
        futures = [ex.submit(fetch, ref) for ref in refs]
        try:
            for fut in cf.as_completed(futures):
                row = fut.result()
                if row:
                    out.append(row)
        except RaptorError:
            # One lookup failed — don't wait on the rest; cancel and let main()
            # report it once.
            for f in futures:
                f.cancel()
            raise
    out.sort(key=lambda r: r["service"])
    if skipped:
        note = ", ".join(
            f"{t}×{n}" for t, n in sorted(skipped.items(), key=lambda x: -x[1])
        )
        print(
            f"facets.py: scanned {len(refs)} deployable resource(s); "
            f"did NOT scan for artifacts: {note}",
            file=sys.stderr,
        )
    print(json.dumps(out, indent=2))


def die(msg):
    print(f"facets.py: {msg}", file=sys.stderr)
    sys.exit(2)


def main():
    if len(sys.argv) < 2:
        die("usage: facets.py projects | envs <project> | services <project>")
    cmd, rest = sys.argv[1], sys.argv[2:]
    try:
        if cmd == "projects":
            cmd_projects()
        elif cmd == "envs" and rest:
            cmd_envs(rest[0])
        elif cmd == "services" and rest:
            cmd_services(rest[0])
        else:
            die("usage: facets.py projects | envs <project> | services <project>")
    except RaptorError as e:
        die(str(e))


if __name__ == "__main__":
    main()
