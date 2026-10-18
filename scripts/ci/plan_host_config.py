#!/usr/bin/env python3
"""Plan the control plane's host-configuration step of a unit release
(docs/adr/20261018133100-host-configuration-lane.md).

The host records what it last converged in
/var/lib/faas/bootstrap/control-plane-roles.json: the commit, every role's
content hash (scripts/ci/host_roles.py) and the environment hash (rendered
inventory and operator variables) of that convergence. This compares the
unit release's commit with that record:

  refuse    the unit release is not built over the previous full release
            (N±1) or over the full release the host runs, or the host's
            record names a commit that is not an ancestor of the release
            commit (it converged newer or diverged content);
  converge  roles whose content hash differs from the record, plus the
            roles whose handlers they notify; every role when the host has
            no record yet, or the record has another format or globals;
  none      every role's hash matches the record.

`gregalectl deploy converge-control-plane --roles` converges the plan's
roles at the release commit; it converges every role instead when the
environment hash moved. After the SLO gate passes, the workflow writes
record.json (with the environment hash gregalectl reports) and the
contract. A rollback converges the same roles at `previous`: the record's
commit, or the host's full release when there is no record.

Writes plan.json and, for converge, record.json into --out. With
--gate-from, writes gate.json instead: the SLO gate for the step, the
platform SLOs as unit host-config and each daemon `deployctl
restart-changed` reported.

Usage:
  plan_host_config.py --units-manifest M --host-state H --out DIR \\
      [--previous-release SHA] [--repo .]
  plan_host_config.py --gate-from RESTART_JSON --out DIR
"""
import argparse
import json
import os
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import host_roles  # noqa: E402
import plan_unit_deploy  # noqa: E402

KIND_RELEASE = "gregale-unit-release"
KIND_RECORD = "gregale-host-config"
GROUP = "control_plane"


class Refused(Exception):
    pass


def git_is_ancestor(ancestor, descendant, root="."):
    out = subprocess.run(["git", "merge-base", "--is-ancestor", ancestor, descendant],
                         cwd=root, capture_output=True, text=True)
    if out.returncode not in (0, 1):
        raise Refused(f"cannot compare {ancestor} with {descendant}: {out.stderr.strip()}")
    return out.returncode == 0


def plan(release, host, new, deps=None, is_ancestor=git_is_ancestor, previous_release=None):
    """new is host_roles.group_hashes at the release commit; deps its
    notify_targets."""
    if release.get("kind") != KIND_RELEASE or release.get("format_version") != 2:
        raise Refused("not a format 2 unit release manifest")
    base, commit = release["base_sha"], release["git_sha"]
    if previous_release and previous_release != base:
        raise Refused(f"unit release is built over {base}, but the previous release before {commit} is "
                      f"{previous_release} (N±1); roll it out in full first")
    if host["base_git_sha"] != base:
        raise Refused(f"host runs full release {host['base_git_sha']}, but this unit release is built over {base}: "
                      "deploy that full release first")
    record = host.get("record")
    usable = (isinstance(record, dict) and record.get("kind") == KIND_RECORD
              and record.get("format_version") == host_roles.FORMAT and record.get("group") == GROUP)
    if usable and record.get("commit") and not is_ancestor(record["commit"], commit):
        raise Refused(f"the control plane converged its roles at {record['commit']}, which is not an ancestor of "
                      f"{commit}: a newer host configuration is already there")
    if usable:
        diff = host_roles.changed(record, new, deps)
        reason = "every role: the record has other globals" if diff["all"] else "roles whose content hash changed"
        previous = record.get("commit") or base
    else:
        diff = {"all": True, "roles": list(new["order"])}
        reason = "every role: the host has no role record yet"
        previous = base
    out = {"action": "converge" if diff["roles"] else "none", "group": GROUP, "commit": commit,
           "previous": previous, "roles": diff["roles"], "all": diff["all"], "reason": reason}
    record_out = None
    if diff["roles"]:
        record_out = {"format_version": host_roles.FORMAT, "kind": KIND_RECORD, "group": GROUP, "commit": commit,
                      "globals": new["globals"], "order": new["order"], "roles": new["roles"]}
    return out, record_out


def gate(report):
    """slo_gate.py argument lists for a host-config step: the platform SLOs
    (no daemon of its own: roles also reload Prometheus, Caddy, PostgreSQL
    settings), then every daemon restart-changed restarted."""
    services = [d["service"] for d in report.get("restarted") or []]
    return [["--unit", "host-config", "--component", "host-config"]] + [plan_unit_deploy.gate_args(s) for s in services]


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--gate-from", help="deployctl restart-changed --json report: write gate.json")
    ap.add_argument("--units-manifest")
    ap.add_argument("--host-state")
    ap.add_argument("--out", required=True)
    ap.add_argument("--previous-release", default="",
                    help="commit of the newest full release tag before the unit release's commit")
    ap.add_argument("--repo", default=".", help="git checkout holding the release commit")
    args = ap.parse_args()
    if args.gate_from:
        with open(args.gate_from, encoding="utf-8") as fh:
            report = json.load(fh)
        os.makedirs(args.out, exist_ok=True)
        with open(os.path.join(args.out, "gate.json"), "w", encoding="utf-8") as fh:
            json.dump({"gate": gate(report)}, fh, indent=2)
            fh.write("\n")
        return 0
    if not args.units_manifest or not args.host_state:
        ap.error("--units-manifest and --host-state are required to plan")
    with open(args.units_manifest, encoding="utf-8") as fh:
        release = json.load(fh)
    with open(args.host_state, encoding="utf-8") as fh:
        host = json.load(fh)
    try:
        tree = host_roles.git_tree(release["git_sha"], args.repo)
        new = host_roles.group_hashes(tree, GROUP)
        result, record = plan(release, host, new, host_roles.notify_targets(tree, new["order"]),
                              lambda a, d: git_is_ancestor(a, d, args.repo), args.previous_release or None)
    except (Refused, RuntimeError, ValueError, KeyError) as err:
        print(f"plan_host_config: refused: {err}", file=sys.stderr)
        return 1
    os.makedirs(args.out, exist_ok=True)
    if record:
        with open(os.path.join(args.out, "record.json"), "w", encoding="utf-8") as fh:
            json.dump(record, fh, indent=2, sort_keys=True)
            fh.write("\n")
    with open(os.path.join(args.out, "plan.json"), "w", encoding="utf-8") as fh:
        json.dump(result, fh, indent=2, sort_keys=True)
        fh.write("\n")
    print(f"plan: host config {result['action']}: {', '.join(result['roles']) or '<none>'} "
          f"({result['reason']}; rollback to {result['previous'][:12]})", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
