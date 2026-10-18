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
            version the host does not run, or (control-plane lane on the
            control plane) the release's data step is not applied yet:
            assemble an overlay over the active release that replaces those
            binaries and bin/migrate, restart only their services, then
            gate them on the SLO gate;
  converge  nothing changed, but the active release replaced units of this
            lane: re-run its activation and gate, so a restart or gate an
            interrupted run never finished still happens;
  none      nothing changed and the active release replaced no unit of this
            lane.

The data step (docs/adr/20261018084150-unit-lane-expand-migrations.md):
a unit release whose range added lane-eligible migrations carries
tools/migrate, built at its commit. The control-plane lane's overlay
replaces bin/migrate with it, and `deployctl deploy` runs bin/migrate
before it activates or restarts anything, as a full release does; a
failed migration stops the lane there. unit-versions.json records the
applied data version. The edge lane refuses a control plane that has not
applied it (deploy order: data, control plane, edge).

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


def overlay_id(current, commit, versions, data_version=None):
    """Deterministic 40-hex release id: a rerun assembles the same directory,
    and retention and rollback treat it like any release."""
    lines = [KIND_RELEASE + "/1", current, commit] + [f"{k}={v}" for k, v in sorted(versions.items())]
    if data_version:
        lines.append(f"@data={data_version}")
    return hashlib.sha1(("\n".join(lines) + "\n").encode()).hexdigest()


def data_of(release):
    """(data version, migrations) the release must apply before restarts;
    (None, []) when its range added no migration."""
    data = release.get("data") or {}
    migrations = data.get("migrations") or []
    if not migrations:
        return None, []
    if not data.get("eligible") or not data.get("version") or not all(m.get("eligible") for m in migrations):
        raise Refused("unit release carries migrations that are not lane-eligible; only a full release applies them")
    if "migrate" not in (release.get("tools") or {}):
        raise Refused("unit release carries migrations but no tools/migrate")
    return data["version"], migrations


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
    data_version, migrations = data_of(release)
    host_data = (previous or {}).get("data_version")
    apply_data = bool(data_version) and host_data != data_version and lane == "control-plane" and host_role == "control-plane"
    if data_version and host_data != data_version and lane == "edge" and host_role == "control-plane":
        raise Refused(f"deploy order: the control plane has not applied this release's migrations ({data_version}); "
                      "deploy its control-plane lane first")
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
    if not changed and not apply_data:
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
    # The data version this host's database has applied, carried forward
    # by every later overlay over it.
    recorded_data = data_version if apply_data else host_data
    if recorded_data:
        record["data_version"] = recorded_data
    replace = {f"bin/{name}": f"bin/{name}" for name in changed}
    replace["bin/deployctl"] = "bin/deployctl"
    replace["unit-versions.json"] = "unit-versions.json"
    if apply_data:
        # deployctl deploy runs the release's bin/migrate before it
        # activates anything; the data tier has its own SLO rules.
        replace["bin/migrate"] = "bin/migrate"
        gate = gate + [["--unit", "migrations"]]
    return {"action": "deploy", "lane": lane, "release_id": overlay_id(current, commit, versions, recorded_data),
            "expect_current": current, "commit": commit, "units": changed, "only": only, "verify": verify,
            "gate": gate, "replace": replace, "unit_versions": record,
            "migrations": [m["path"] for m in migrations] if apply_data else []}


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
