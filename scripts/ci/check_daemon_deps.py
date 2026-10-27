#!/usr/bin/env python3
"""Fail when a release binary links a package its rule forbids.

A per-unit release (docs/adr/1650-per-unit-control-plane-releases.md)
ships a binary whenever a file in its import closure changes, so every
package a binary links widens the set of fixes that restart it. Leaf
packages (pkg/storagekey and the like) keep, for example, imaged and
builderd from linking the scheduler engine; this check keeps them from
drifting back. scripts/ci/daemon_deps.json lists the rules:

  {"rules": [{"package": "pkg/sched", "binaries": ["imaged"], "reason": "..."}]}

A package with a dot in its first path element is an import path
(golang.org/x/net/trace); one without is a standard library package when
the binary loads one of that name and the module has none (text/template),
and otherwise relative to the module (pkg/sched).
"binaries": "DAEMONS" names every daemon in the Makefile, less the rule's
"except" list. "allow_importers" names packages that may import the rule's
package (a library that only parses a template, say): the binary violates
the rule only when another package imports it. A rule's package matches
itself only, not its subpackages (pkg/sched/flowcount is a separate leaf). Closures are loaded like affected_units.py loads them: `go list
-deps` under the release build configuration (linux/amd64, CGO off, the
Makefile's DAEMON_TAGS for daemons). A violation prints the import chain
that pulls the package in.

Usage:
  scripts/ci/check_daemon_deps.py [--config scripts/ci/daemon_deps.json]
"""
import argparse
import json
import os
import subprocess
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


def import_chain(graph, main, target, allowed_importers=frozenset()):
    """The shortest import path from main to target, or None. An import of
    target by one of allowed_importers does not count."""
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
            if imp == target and pkg in allowed_importers:
                continue
            if imp in graph and imp not in prev:
                prev[imp] = pkg
                queue.append(imp)
    return None


def import_path(package, module, graph=None):
    """A rule's package as an import path, in the binary whose package
    graph is graph (see the module docstring)."""
    if "." in package.split("/")[0]:
        return package
    if graph is not None and package in graph and f"{module}/{package}" not in graph:
        return package
    return f"{module}/{package}"


def rule_binaries(rule, daemons=()):
    """The binaries a rule covers."""
    if rule["binaries"] == "DAEMONS":
        return [d for d in daemons if d not in set(rule.get("except", ()))]
    return list(rule["binaries"])


def violations(rules, graphs, module, daemons=()):
    """[(rule, binary, chain)] for every binary that links a forbidden
    package. graphs maps each binary to the package graph it was loaded
    from ({import path: {"imports": set}})."""
    out = []
    for rule in rules:
        for name in rule_binaries(rule, daemons):
            if name not in graphs:
                raise ValueError(f"rule for {rule['package']} names {name}, which is not a release binary")
            graph = graphs[name]
            target = import_path(rule["package"], module, graph)
            allowed = frozenset(import_path(p, module, graph) for p in rule.get("allow_importers", ()))
            chain = import_chain(graph, f"{module}/cmd/{name}", target, allowed)
            if chain:
                out.append((rule, name, [p.removeprefix(module + "/") for p in chain]))
    return out


def go_graph(mains, tags, root="."):
    """Every package reachable from mains under the release build
    configuration, standard library and modules included:
    {import path: {"imports": set}}."""
    env = dict(os.environ, GOOS="linux", GOARCH="amd64", CGO_ENABLED="0")
    cmd = [os.environ.get("GO", "go"), "list", "-deps", "-json=ImportPath,Imports"]
    if tags:
        cmd.append("-tags=" + ",".join(tags))
    out = subprocess.run(cmd + mains, check=True, capture_output=True, text=True, cwd=root, env=env).stdout
    return {obj["ImportPath"]: {"imports": set(obj.get("Imports") or [])} for obj in au.decode_stream(out)}


def load_graphs(root="."):
    """({binary: package graph}, module, daemons) for the release binaries."""
    with open(os.path.join(root, "Makefile"), encoding="utf-8") as fh:
        makefile = fh.read()
    module = au.module_path(root)
    daemons = au.makefile_list(makefile, "DAEMONS")
    graphs = {}
    for names, tags in ((daemons, au.daemon_tags(makefile)), (au.makefile_list(makefile, "CLIS"), [])):
        graph = go_graph([f"./cmd/{n}" for n in names], tags, root)
        graphs.update({n: graph for n in names})
    return graphs, module, daemons


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--config", default=DEFAULT_CONFIG, help="rules (default scripts/ci/daemon_deps.json)")
    args = ap.parse_args()
    rules = load_rules(args.config)
    graphs, module, daemons = load_graphs()
    found = violations(rules, graphs, module, daemons)
    for rule, name, chain in found:
        print(f"{name} links {rule['package']}: {' -> '.join(chain)}\n  {rule['reason']}", file=sys.stderr)
    if found:
        return 1
    pairs = sum(len(rule_binaries(r, daemons)) for r in rules)
    print(f"daemon deps: {pairs} binary/package rules hold")
    return 0


if __name__ == "__main__":
    sys.exit(main())
