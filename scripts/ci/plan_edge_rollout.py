#!/usr/bin/env python3
"""Order the edge lane's hosts canary first and pick each host's SLO gate
(docs/adr/1650-per-unit-control-plane-releases.md).

Each lane deploys its first host as the canary, with the full SLO gate, and
every later host with the short follower gate. A failure on any host rolls
that host back and stops the lane. The control-plane lane has a single host,
the control plane, so it is always the canary. In the edge lane:

  * when a compute node restarts a gateway (gatewayd-internal), the canary
    is the designated compute node (--canary, default the first target). It
    deploys first, the control plane's gatewayd-public follows, then the
    other nodes;
  * when only the control plane restarts one (a gatewayd-public-only
    release, whose compute unit is the optional gatewayd-public-edge), the
    control plane is the canary and every node follows.

The gate durations live in scripts/ci/release_units.json ("gates"). The
budget below adds them to measured per-host costs; a test keeps a release
that changes both lanes on three nodes within 30 minutes.

Usage:
  plan_edge_rollout.py --units-manifest M --targets JSON [--canary NODE]
  plan_edge_rollout.py --budget [--nodes 3]
Prints {"canary": target|null, "followers": [targets], "control_plane":
{"deploy": bool, "gate": "canary"|"follower"}} as JSON.
"""
import argparse
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import slo_gate  # noqa: E402

GATE_KINDS = ("canary", "follower")

# Minutes outside the gates, from the slice 1/2 measurements and the job
# structure (docs/adr/1650-per-unit-control-plane-releases.md, time budget):
# the release-units build on a hosted runner with the main-branch cache, the
# edge lane's plan job, and one host's deploy job (artifact download and
# verification, checkout, plan, stage, overlay, restart and readiness).
BUILD_MINUTES = 4.5
EDGE_PLAN_MINUTES = 1.0
HOST_DEPLOY_MINUTES = 2.0
BUDGET_MINUTES = 30.0


class PlanError(Exception):
    pass


def load_gates(path=os.path.join(HERE, "release_units.json")):
    with open(path, encoding="utf-8") as fh:
        gates = json.load(fh)["gates"]
    out = {}
    for kind in GATE_KINDS:
        try:
            duration = slo_gate.parse_duration(gates[kind]["duration"])
            hold = slo_gate.parse_duration(gates[kind]["max_hold"])
        except Exception as err:  # argparse.ArgumentTypeError for a bad duration
            raise PlanError(f"gate {kind}: {err}") from None
        if not 0 < hold < duration:
            raise PlanError(f"gate {kind}: max_hold must be positive and shorter than duration")
        out[kind] = {"duration": gates[kind]["duration"], "max_hold": gates[kind]["max_hold"],
                     "minutes": duration / 60}
    return out


def edge_units(manifest):
    """(units a compute node restarts, units the control plane restarts)
    among the release's edge units; optional services do not count."""
    node, cp = set(), set()
    for name, unit in manifest["units"].items():
        if unit["lane"] != "edge":
            continue
        for service in unit["services"]:
            if service.get("optional"):
                continue
            (node if service.get("host") == "compute-only" else cp).add(name)
    return sorted(node), sorted(cp)


def touches_nodes(manifest):
    return any(service.get("host") == "compute-only"
               for unit in manifest["units"].values() if unit["lane"] == "edge"
               for service in unit["services"])


def plan(manifest, targets, canary=""):
    node_units, cp_units = edge_units(manifest)
    nodes = targets if touches_nodes(manifest) else []
    names = [t["node"] for t in nodes]
    if len(set(names)) != len(names):
        raise PlanError("edge targets name a node twice")
    chosen = None
    if node_units and nodes:
        if canary:
            if canary not in names:
                raise PlanError(f"canary {canary!r} is not one of the edge targets: {', '.join(names)}")
            chosen = nodes[names.index(canary)]
        else:
            chosen = nodes[0]
    elif node_units:
        raise PlanError(f"{', '.join(node_units)} restart on compute nodes: list the edge targets")
    followers = [t for t in nodes if t is not chosen]
    return {
        "canary": chosen,
        "followers": followers,
        "control_plane": {"deploy": bool(cp_units), "gate": "follower" if chosen else "canary"},
    }


def budget_minutes(gates, nodes, control_plane_lane=True, edge_control_plane=True):
    """Estimated minutes from merge to the last host promoted, for a
    release whose edge lane restarts gateways on the control plane and on
    `nodes` compute nodes (the first a canary)."""
    total = BUILD_MINUTES
    if control_plane_lane:
        total += HOST_DEPLOY_MINUTES + gates["canary"]["minutes"]
    total += EDGE_PLAN_MINUTES
    hosts = nodes + (1 if edge_control_plane else 0)
    if hosts:
        total += hosts * HOST_DEPLOY_MINUTES + gates["canary"]["minutes"] + (hosts - 1) * gates["follower"]["minutes"]
    return total


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--units-manifest")
    ap.add_argument("--targets", default="[]", help="JSON array of edge targets")
    ap.add_argument("--canary", default="", help="node to deploy first (default: the first target)")
    ap.add_argument("--budget", action="store_true", help="print the time budget instead")
    ap.add_argument("--nodes", type=int, default=3)
    args = ap.parse_args()
    try:
        gates = load_gates()
        if args.budget:
            minutes = budget_minutes(gates, args.nodes)
            print(f"both lanes, control plane + {args.nodes} compute nodes: {minutes:.1f} min "
                  f"(budget {BUDGET_MINUTES:.0f})")
            return 0 if minutes <= BUDGET_MINUTES else 1
        with open(args.units_manifest, encoding="utf-8") as fh:
            manifest = json.load(fh)
        result = plan(manifest, json.loads(args.targets), args.canary)
    except (PlanError, KeyError, ValueError) as err:
        print(f"plan_edge_rollout: {err}", file=sys.stderr)
        return 1
    json.dump(result, sys.stdout, sort_keys=True)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
