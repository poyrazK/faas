#!/usr/bin/env python3
"""Unit tests for check_daemon_deps.py (forbidden packages per release binary)."""
import importlib.util
import json
import pathlib
import subprocess
import tempfile
import unittest

HERE = pathlib.Path(__file__).parent
REPO = HERE.parent.parent
spec = importlib.util.spec_from_file_location("check_daemon_deps", HERE / "check_daemon_deps.py")
cdd = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cdd)
au = cdd.au

M = "example.com/m"


def graph(edges):
    """An affected_units.go_graph-shaped graph from {pkg: [imports]}."""
    pkgs = set(edges) | {i for imps in edges.values() for i in imps}
    return {f"{M}/{p}": {"imports": {f"{M}/{i}" for i in edges.get(p, ())}} for p in pkgs}


GRAPH = graph({
    "cmd/vmmd": ["pkg/vmmdgrpc", "pkg/storagekey"],
    "pkg/vmmdgrpc": ["pkg/sched/flowcount", "pkg/lease"],
    "pkg/lease": ["pkg/sched"],
    "cmd/imaged": ["pkg/storagekey"],
    "pkg/sched": ["pkg/storagekey"],
})
RULE = {"package": "pkg/sched", "binaries": ["vmmd", "imaged"], "reason": "keep the engine out"}


class ViolationsTest(unittest.TestCase):
    def test_reports_the_shortest_chain(self):
        got = cdd.violations([RULE], {"vmmd": GRAPH, "imaged": GRAPH}, M)
        self.assertEqual(got, [(RULE, "vmmd", ["cmd/vmmd", "pkg/vmmdgrpc", "pkg/lease", "pkg/sched"])])

    def test_subpackages_are_separate_packages(self):
        rule = dict(RULE, package="pkg/sched/flowcount", binaries=["imaged"])
        self.assertEqual(cdd.violations([rule], {"imaged": GRAPH}, M), [])

    def test_unknown_binary_is_an_error(self):
        with self.assertRaisesRegex(ValueError, "not a release binary"):
            cdd.violations([RULE], {"vmmd": GRAPH}, M)

    def test_import_path_packages_and_every_daemon(self):
        g = graph({"cmd/vmmd": ["pkg/grpcish"], "cmd/imaged": []})
        g[f"{M}/pkg/grpcish"]["imports"].add("golang.org/x/net/trace")
        g["golang.org/x/net/trace"] = {"imports": set()}
        rule = {"package": "golang.org/x/net/trace", "binaries": "DAEMONS", "reason": "r"}
        got = cdd.violations([rule], {"vmmd": g, "imaged": g}, M, daemons=["vmmd", "imaged"])
        self.assertEqual(got, [(rule, "vmmd", ["cmd/vmmd", "pkg/grpcish", "golang.org/x/net/trace"])])

    def test_except_and_allowed_importers(self):
        g = graph({"cmd/apid": ["pkg/dashboard"], "cmd/vmmd": ["pkg/migrate"], "cmd/imaged": ["pkg/page"]})
        g[f"{M}/pkg/dashboard"]["imports"].add("text/template")
        g[f"{M}/pkg/migrate"]["imports"].add("github.com/pressly/goose/v3")
        g["github.com/pressly/goose/v3"] = {"imports": {"text/template"}}
        g[f"{M}/pkg/page"]["imports"].add("text/template")
        g["text/template"] = {"imports": set()}
        rule = {"package": "text/template", "binaries": "DAEMONS", "except": ["apid"],
                "allow_importers": ["github.com/pressly/goose/v3"], "reason": "r"}
        got = cdd.violations([rule], {"apid": g, "vmmd": g, "imaged": g}, M, daemons=["apid", "vmmd", "imaged"])
        self.assertEqual(got, [(rule, "imaged", ["cmd/imaged", "pkg/page", "text/template"])])

    def test_chain_ignores_packages_outside_the_graph(self):
        g = graph({"cmd/x": ["pkg/a"]})
        g[f"{M}/pkg/a"]["imports"].add("golang.org/x/sync/errgroup")
        self.assertIsNone(cdd.import_chain(g, f"{M}/cmd/x", f"{M}/pkg/sched"))


class RulesTest(unittest.TestCase):
    def test_rules_need_every_field(self):
        with tempfile.NamedTemporaryFile("w", suffix=".json") as fh:
            json.dump({"rules": [{"package": "pkg/sched", "binaries": ["vmmd"]}]}, fh)
            fh.flush()
            with self.assertRaisesRegex(ValueError, "package, binaries and reason"):
                cdd.load_rules(fh.name)

    def test_repository_rules_name_release_binaries(self):
        makefile = (REPO / "Makefile").read_text(encoding="utf-8")
        binaries = set(au.makefile_list(makefile, "DAEMONS")) | set(au.makefile_list(makefile, "CLIS"))
        goroot = subprocess.run(["go", "env", "GOROOT"], capture_output=True, text=True, check=True).stdout.strip()
        for rule in cdd.load_rules(cdd.DEFAULT_CONFIG):
            if "." not in rule["package"].split("/")[0]:
                # A module package, or else a standard library one.
                self.assertTrue((REPO / rule["package"]).is_dir() or
                                (pathlib.Path(goroot) / "src" / rule["package"]).is_dir(), rule["package"])
            if rule["binaries"] != "DAEMONS":
                self.assertLessEqual(set(rule["binaries"]), binaries, rule["package"])
            self.assertLessEqual(set(rule.get("except", ())), binaries, rule["package"])


if __name__ == "__main__":
    unittest.main()
