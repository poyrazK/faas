#!/usr/bin/env python3
"""Plan a per-unit control-plane deploy from a signed unit release and the
host's state (docs/adr/1650-per-unit-control-plane-releases.md).

A unit release is cumulative: it carries every control-plane daemon whose
inputs changed between its base full release and its commit, each stamped
with its unit version. The host runs either that base full release or an
earlier unit release over it, and records unit versions in the active
release's unit-versions.json. The plan:

  refuse    the host runs another full release than the unit release's base,
            or a unit release whose commit is not an ancestor of this one
            (a newer or diverged deploy), or one listing a unit this release
            does not carry;
  deploy    some carried unit's version differs from the host's: assemble an
            overlay over the active release that replaces those binaries and
            restart only their services;
  converge  nothing changed, but the active release is a unit release:
            re-run its activation so a restart that an interrupted run never
            made still happens (deployctl's converged path checks which
            processes still run older binaries);
  none      nothing changed and the host runs the full release.

Writes plan.json and, for deploy, unit-versions.json into --out.

Usage:
  plan_unit_deploy.py --units-manifest M --host-state H --out DIR [--expect-units apid,meterd]
"""
import argparse
import hashlib
import json
import os
import subprocess
import sys

KIND_RELEASE = "gregale-unit-release"
KIND_VERSIONS = "gregale-unit-versions"


class Refused(Exception):
    pass


def git_is_ancestor(ancestor, descendant, root="."):
    out = subprocess.run(["git", "merge-base", "--is-ancestor", ancestor, descendant],
                         cwd=root, capture_output=True, text=True)
    if out.returncode not in (0, 1):
        raise Refused(f"cannot compare {ancestor} with {descendant}: {out.stderr.strip()}")
    return out.returncode == 0


def overlay_id(current, commit, versions):
    """Deterministic 40-hex release id: a rerun assembles the same directory,
    and retention and rollback treat it like any release."""
    body = "\n".join([KIND_RELEASE + "/1", current, commit] + [f"{k}={v}" for k, v in sorted(versions.items())])
    return hashlib.sha1((body + "\n").encode()).hexdigest()


def plan(release, host, is_ancestor=git_is_ancestor, expect=None):
    if release.get("kind") != KIND_RELEASE or release.get("format_version") != 1:
        raise Refused("not a format 1 unit release manifest")
    base, commit = release["base_sha"], release["git_sha"]
    if host["base_git_sha"] != base:
        raise Refused(f"host runs full release {host['base_git_sha']}, but this unit release is built over {base}: "
                      "deploy that full release first, or rebuild the unit release with base set to the host's")
    current = host["current"]
    previous = host.get("unit_versions") or None
    host_versions = {}
    if previous:
        if previous.get("kind") != KIND_VERSIONS or previous.get("base_sha") != base:
            raise Refused(f"active release {current} records unit versions for another base")
        if not is_ancestor(previous["git_sha"], commit):
            raise Refused(f"host runs a unit release built at {previous['git_sha']}, which is not an ancestor of {commit}")
        host_versions = previous["units"]
        missing = sorted(set(host_versions) - set(release["units"]))
        if missing:
            raise Refused(f"host runs unit versions this release does not carry: {', '.join(missing)}")

    versions = {name: unit["version"] for name, unit in release["units"].items()}
    changed = sorted(name for name, version in versions.items() if host_versions.get(name) != version)
    if not changed:
        if previous:
            return {"action": "converge", "release_id": current, "expect_current": current,
                    "only": previous["services"], "verify": previous["verify"], "replace": {}}
        return {"action": "none", "release_id": current, "only": [], "verify": [], "replace": {}}
    if expect is not None and set(changed) != set(expect):
        raise Refused(f"unit release would restart {', '.join(changed)}, not the expected {', '.join(sorted(expect))}")

    only, verify = [], []
    for name in changed:
        for service in release["units"][name]["services"]:
            spec = ("?" if service.get("optional") else "") + service["name"]
            if service.get("readyz"):
                spec += "=" + service["readyz"]
            verify.append(spec)
            if not service.get("optional"):
                only.append(service["name"])
    release_id = overlay_id(current, commit, versions)
    record = {
        "format_version": 1,
        "kind": KIND_VERSIONS,
        "base_sha": base,
        "built_over": current,
        "git_sha": commit,
        "units": versions,
        "replaced": changed,
        "services": only,
        "verify": verify,
    }
    replace = {f"bin/{name}": f"bin/{name}" for name in changed}
    replace["bin/deployctl"] = "bin/deployctl"
    replace["unit-versions.json"] = "unit-versions.json"
    return {"action": "deploy", "release_id": release_id, "expect_current": current, "commit": commit,
            "changed": changed, "only": only, "verify": verify, "replace": replace, "unit_versions": record}


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--units-manifest", required=True)
    ap.add_argument("--host-state", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--expect-units", default="", help="comma-separated units the deploy must restart")
    ap.add_argument("--repo", default=".", help="git checkout used for ancestry checks")
    args = ap.parse_args()
    with open(args.units_manifest, encoding="utf-8") as fh:
        release = json.load(fh)
    with open(args.host_state, encoding="utf-8") as fh:
        host = json.load(fh)
    expect = [u for u in args.expect_units.split(",") if u] or None
    try:
        result = plan(release, host, lambda a, d: git_is_ancestor(a, d, args.repo), expect)
    except Refused as err:
        print(f"plan_unit_deploy: refused: {err}", file=sys.stderr)
        return 1
    os.makedirs(args.out, exist_ok=True)
    record = result.pop("unit_versions", None)
    if record:
        with open(os.path.join(args.out, "unit-versions.json"), "w", encoding="utf-8") as fh:
            json.dump(record, fh, indent=2, sort_keys=True)
            fh.write("\n")
    with open(os.path.join(args.out, "plan.json"), "w", encoding="utf-8") as fh:
        json.dump(result, fh, indent=2, sort_keys=True)
        fh.write("\n")
    print(f"plan: {result['action']} release={result['release_id']} restart={','.join(result['only']) or '<none>'}",
          file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
