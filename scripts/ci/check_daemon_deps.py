#!/usr/bin/env python3
"""Fail when a release binary links a package its rule forbids.

A per-unit release (docs/adr/1650-per-unit-control-plane-releases.md)
ships a binary whenever a file in its import closure changes, so every
package a binary links widens the set of fixes that restart it. Leaf
packages (pkg/storagekey and the like) keep, for example, imaged and
builderd from linking the scheduler engine; this check keeps them from
drifting back. scripts/ci/daemon_deps.json lists the rules:

  {"rules": [{"package": "pkg/sched", "binaries": ["imaged"], "reason": "..."}]}

A rule's package matches itself only, not its subpackages (pkg/sched/flowcount
is a separate leaf). Closures are loaded like affected_units.py loads them:
`go list -deps` under the release build configuration (linux/amd64, CGO
off, -tags metal for daemons). A violation prints the import chain that
pulls the package in.

Usage:
  scripts/ci/check_daemon_deps.py [--config scripts/ci/daemon_deps.json]
"""
import argparse
import json
import os
import sys
from collections import deque

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import affected_units as au  # noqa: E402

DEFAULT_CONFIG = os.path.join(HERE, "daemon_deps.json")


def load_rules(path):
    with open(path, encoding="utf-8") as fh:
        rules = json.load(fh)["rules"]
    for rule in rules:
        if not rule.get("package") or not rule.get("binaries") or not rule.get("reason"):
            raise ValueError(f"{path}: every rule needs package, binaries and reason: {rule}")
    return rules


def import_chain(graph, main, target):
    """The shortest import path from main to target, or None."""
    prev, queue = {main: None}, deque([main])
    while queue:
        pkg = queue.popleft()
        if pkg == target:
            chain = []
            while pkg is not None:
                chain.append(pkg)
                pkg = prev[pkg]
            return chain[::-1]
        for imp in sorted((graph.get(pkg) or {}).get("imports", ())):
            if imp in graph and imp not in prev:
                prev[imp] = pkg
                queue.append(imp)
    return None


def violations(rules, graphs, module):
    """[(rule, binary, chain)] for every binary that links a forbidden
    package. graphs maps each binary to the package graph it was loaded
    from (affected_units.go_graph)."""
    out = []
    for rule in rules:
        target = f"{module}/{rule['package']}"
        for name in rule["binaries"]:
            if name not in graphs:
                raise ValueError(f"rule for {rule['package']} names {name}, which is not a release binary")
            chain = import_chain(graphs[name], f"{module}/cmd/{name}", target)
            if chain:
                out.append((rule, name, [p.removeprefix(module + "/") for p in chain]))
    return out


def load_graphs(root="."):
    with open(os.path.join(root, "Makefile"), encoding="utf-8") as fh:
        makefile = fh.read()
    module = au.module_path(root)
    graphs = {}
    for names, tags in ((au.makefile_list(makefile, "DAEMONS"), ["metal"]),
                        (au.makefile_list(makefile, "CLIS"), [])):
        graph = au.go_graph([f"./cmd/{n}" for n in names], tags, root)
        graphs.update({n: graph for n in names})
    return graphs, module


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--config", default=DEFAULT_CONFIG, help="rules (default scripts/ci/daemon_deps.json)")
    args = ap.parse_args()
    rules = load_rules(args.config)
    graphs, module = load_graphs()
    found = violations(rules, graphs, module)
    for rule, name, chain in found:
        print(f"{name} links {rule['package']}: {' -> '.join(chain)}\n  {rule['reason']}", file=sys.stderr)
    if found:
        return 1
    print(f"daemon deps: {sum(len(r['binaries']) for r in rules)} binary/package rules hold")
    return 0


if __name__ == "__main__":
    sys.exit(main())
