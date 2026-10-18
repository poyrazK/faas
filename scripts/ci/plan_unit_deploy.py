#!/usr/bin/env python3
"""Plan one host's part of a per-unit deploy from a signed unit release and
the host's state (docs/adr/1650-per-unit-control-plane-releases.md).

A unit release is cumulative: it carries every daemon of an eligible lane
(control-plane, then edge) whose inputs changed between its base full
release and its commit, each stamped with its unit version. A host runs
either that base full release or earlier unit releases over it, and records
unit versions in the active release's unit-versions.json. This plans one
lane on one host role:

  refuse    the unit release's base is not the previous full release (the
            N±1 rule: CI checked these units against that release only), or
            the host runs another full release, or a unit release whose
            commit is not an ancestor of this one (newer or diverged), or
            one listing a unit of this lane that this release does not
            carry;
  deploy    a carried unit of this lane that runs on this host role has a
            version the host does not run: assemble an overlay over the
            active release that replaces those binaries, restart only their
            services, then gate them on the SLO gate;
  converge  nothing changed, but the active release replaced units of this
            lane: re-run its activation and gate, so a restart or gate an
            interrupted run never finished still happens;
  none      nothing changed and the active release replaced no unit of this
            lane.

Writes plan.json and, for deploy, unit-versions.json into --out.

Usage:
  plan_unit_deploy.py --units-manifest M --host-state H --out DIR \\
      --lane control-plane|edge --host-role control-plane|compute-only \\
      [--previous-release SHA] [--expect-units apid,meterd]
"""
import argparse
import hashlib
import json
import os
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import slo_gate  # noqa: E402

KIND_RELEASE = "gregale-unit-release"
KIND_VERSIONS = "gregale-unit-versions"
FORMAT = 2
LANES = ("control-plane", "edge")
HOST_ROLES = ("control-plane", "compute-only")


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


def host_services(unit, host_role):
    """The unit's services that run on this host role (control-plane when a
    service names none)."""
    return [s for s in unit["services"] if s.get("host", "control-plane") == host_role]


def gate_args(name):
    """slo_gate.py arguments for one unit; a unit the gate does not know is
    judged by the platform SLOs and its own alert component."""
    if name in slo_gate.UNITS:
        return ["--unit", name]
    return ["--unit", name, "--component", name]


def activation(release, names, host_role):
    """(services to restart, verify specs, gate argument lists) for units."""
    only, verify, gate = [], [], []
    for name in names:
        for service in host_services(release["units"][name], host_role):
            spec = ("?" if service.get("optional") else "") + service["name"]
            if service.get("readyz"):
                spec += "=" + service["readyz"]
            verify.append(spec)
            if not service.get("optional"):
                only.append(service["name"])
        gate.append(gate_args(name))
    return only, verify, gate


def plan(release, host, lane, host_role, is_ancestor=git_is_ancestor, expect=None, previous_release=None):
    if release.get("kind") != KIND_RELEASE or release.get("format_version") != FORMAT:
        raise Refused(f"not a format {FORMAT} unit release manifest")
    if lane not in LANES or host_role not in HOST_ROLES:
        raise Refused(f"unknown lane {lane!r} or host role {host_role!r}")
    base, commit = release["base_sha"], release["git_sha"]
    if previous_release and previous_release != base:
        raise Refused(f"unit release is built over {base}, but the previous release before {commit} is "
                      f"{previous_release}: CI checked these units against that release only (N±1); "
                      "roll it out in full first")
    if host["base_git_sha"] != base:
        raise Refused(f"host runs full release {host['base_git_sha']}, but this unit release is built over {base}: "
                      "deploy that full release first")
    current = host["current"]
    previous = host.get("unit_versions") or None
    host_versions, host_lanes = {}, {}
    if previous:
        if previous.get("kind") != KIND_VERSIONS or previous.get("base_sha") != base:
            raise Refused(f"active release {current} records unit versions for another base")
        if not is_ancestor(previous["git_sha"], commit):
            raise Refused(f"host runs a unit release built at {previous['git_sha']}, which is not an ancestor of {commit}")
        host_versions = previous["units"]
        host_lanes = previous.get("unit_lanes") or {}

    scope = sorted(name for name, unit in release["units"].items()
                   if unit["lane"] == lane and host_services(unit, host_role))
    missing = sorted(name for name, unit_lane in host_lanes.items()
                     if unit_lane == lane and name in host_versions and name not in scope)
    if missing:
        raise Refused(f"host runs {lane} unit versions this release does not carry: {', '.join(missing)}")

    changed = [name for name in scope if host_versions.get(name) != release["units"][name]["version"]]
    if not changed:
        replaced = [n for n in (previous or {}).get("replaced", []) if n in scope]
        if replaced:
            only, verify, gate = activation(release, replaced, host_role)
            return {"action": "converge", "lane": lane, "release_id": current, "expect_current": current,
                    "units": replaced, "only": only, "verify": verify, "gate": gate, "replace": {}}
        return {"action": "none", "lane": lane, "release_id": current, "units": [], "only": [], "verify": [],
                "gate": [], "replace": {}}
    if expect is not None and set(changed) != set(expect):
        raise Refused(f"unit release would restart {', '.join(changed)}, not the expected {', '.join(sorted(expect))}")

    versions = dict(host_versions)
    lanes = dict(host_lanes)
    for name in scope:
        versions[name] = release["units"][name]["version"]
        lanes[name] = lane
    only, verify, gate = activation(release, changed, host_role)
    record = {
        "format_version": 1,
        "kind": KIND_VERSIONS,
        "base_sha": base,
        "built_over": current,
        "git_sha": commit,
        "units": versions,
        "unit_lanes": lanes,
        "replaced": changed,
    }
    replace = {f"bin/{name}": f"bin/{name}" for name in changed}
    replace["bin/deployctl"] = "bin/deployctl"
    replace["unit-versions.json"] = "unit-versions.json"
    return {"action": "deploy", "lane": lane, "release_id": overlay_id(current, commit, versions),
            "expect_current": current, "commit": commit, "units": changed, "only": only, "verify": verify,
            "gate": gate, "replace": replace, "unit_versions": record}


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--units-manifest", required=True)
    ap.add_argument("--host-state", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--lane", required=True, choices=LANES)
    ap.add_argument("--host-role", required=True, choices=HOST_ROLES)
    ap.add_argument("--previous-release", default="",
                    help="commit of the newest full release tag before the unit release's commit")
    ap.add_argument("--expect-units", default="", help="comma-separated units the deploy must restart")
    ap.add_argument("--repo", default=".", help="git checkout used for ancestry checks")
    args = ap.parse_args()
    with open(args.units_manifest, encoding="utf-8") as fh:
        release = json.load(fh)
    with open(args.host_state, encoding="utf-8") as fh:
        host = json.load(fh)
    expect = [u for u in args.expect_units.split(",") if u] or None
    try:
        result = plan(release, host, args.lane, args.host_role, lambda a, d: git_is_ancestor(a, d, args.repo),
                      expect, args.previous_release or None)
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
    print(f"plan: {args.lane} on {args.host_role}: {result['action']} release={result['release_id']} "
          f"restart={','.join(result['only']) or '<none>'}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
