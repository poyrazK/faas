#!/usr/bin/env python3
"""Unit tests for affected_units.py (per-unit release selection)."""
import importlib.util
import json
import os
import pathlib
import re
import shutil
import subprocess
import tempfile
import unittest

HERE = pathlib.Path(__file__).parent
REPO = HERE.parent.parent
spec = importlib.util.spec_from_file_location("affected_units", HERE / "affected_units.py")
au = importlib.util.module_from_spec(spec)
spec.loader.exec_module(au)

CONFIG = {
    "binaries": {
        "apid": {"lane": "control-plane", "services": [{"name": "apid"}]},
        "meterd": {"lane": "control-plane", "services": [{"name": "meterd"}]},
        "schedd": {"lane": "shared"},
        "gatewayd-internal": {"lane": "edge", "services": [{"name": "gatewayd-internal", "host": "compute-only"}]},
        "vmmd": {"lane": "compute"},
        "gregalectl": {"lane": "tool"},
    },
    "global_inputs": ["go.mod", "go.sum"],
    "blocking_paths": {
        "migrations": ["migrations/"],
        "deploy": ["deploy/"],
        "control_plane_bundle": ["docs/DPA.md"],
    },
    "pending_paths": {"guest": ["guest/"]},
    "compute_only_paths": ["deploy/ansible/roles/vmmd_service/"],
}

# (package dirs, input files) per binary, as binary_closures returns them.
CLOSURES = {
    "apid": ({"cmd/apid", "pkg/apid", "pkg/state"},
             {"cmd/apid/main.go", "pkg/apid/server.go", "pkg/apid/static/index.html", "pkg/state/store.go"}),
    "meterd": ({"cmd/meterd", "pkg/state"}, {"cmd/meterd/main.go", "pkg/state/store.go"}),
    "schedd": ({"cmd/schedd", "pkg/sched", "pkg/state"},
               {"cmd/schedd/main.go", "pkg/sched/sched.go", "pkg/state/store.go"}),
    "gatewayd-internal": ({"cmd/gatewayd-internal", "pkg/gateway", "pkg/state"},
                          {"cmd/gatewayd-internal/main.go", "pkg/gateway/proxy.go", "pkg/state/store.go"}),
    "vmmd": ({"cmd/vmmd", "pkg/fcvm"}, {"cmd/vmmd/main.go", "pkg/fcvm/vm_linux.go"}),
    "gregalectl": ({"cmd/gregalectl", "pkg/state"}, {"cmd/gregalectl/main.go", "pkg/state/store.go"}),
}

EXISTS = set().union(*(files for _, files in CLOSURES.values())) | {
    "pkg/fcvm/vm_darwin.go", "pkg/apid/server_test.go", "docs/quickstart.md", "migrations/00001_init.sql",
}


def changed(rep):
    return {name: b["commit"] for name, b in rep["binaries"].items() if b["changed"]}


class AnalyzeTest(unittest.TestCase):
    CASES = [
        # name, commits (newest first), changed binaries, deployable (eligible lanes), blocked_by, eligible
        ("docs only", [("c1", ["docs/quickstart.md"])], {}, [], [], False),
        ("apid-only package", [("c1", ["pkg/apid/server.go"])], {"apid": "c1"}, ["apid"], [], True),
        ("embedded file", [("c1", ["pkg/apid/static/index.html"])], {"apid": "c1"}, ["apid"], [], True),
        ("test file never ships", [("c1", ["pkg/apid/server_test.go"])], {}, [], [], False),
        ("shared package changes every importer",
         [("c1", ["pkg/state/store.go"])],
         {"apid": "c1", "meterd": "c1", "schedd": "c1", "gatewayd-internal": "c1", "gregalectl": "c1"},
         ["apid", "gatewayd-internal", "meterd"], [], True),
        ("compute-only change has no control-plane unit",
         [("c1", ["pkg/fcvm/vm_linux.go"])], {"vmmd": "c1"}, [], [], False),
        ("constrained-out file is not an input", [("c1", ["pkg/fcvm/vm_darwin.go"])], {}, [], [], False),
        ("deleted file under a closure package",
         [("c1", ["pkg/apid/removed.go"])], {"apid": "c1"}, ["apid"], [], True),
        ("deleted test file is not an input", [("c1", ["pkg/apid/gone_test.go"])], {}, [], [], False),
        ("go.mod changes every binary",
         [("c1", ["go.mod"])],
         {"apid": "c1", "meterd": "c1", "schedd": "c1", "gatewayd-internal": "c1", "vmmd": "c1", "gregalectl": "c1"},
         ["apid", "gatewayd-internal", "meterd"], [], True),
        ("migration blocks the lane",
         [("c2", ["pkg/apid/server.go"]), ("c1", ["migrations/00002_add.sql"])],
         {"apid": "c2"}, [], ["migrations"], False),
        ("migration test does not block",
         [("c1", ["pkg/apid/server.go", "migrations/00002_add_test.go"])], {"apid": "c1"}, ["apid"], [], True),
        ("deploy tree blocks", [("c1", ["pkg/apid/server.go", "deploy/ansible/site.yml"])],
         {"apid": "c1"}, [], ["deploy"], False),
        ("bundle file blocks", [("c1", ["docs/DPA.md"])], {}, [], ["control_plane_bundle"], False),
        ("guest change is pending, not blocking", [("c1", ["pkg/apid/server.go", "guest/init/main.go"])],
         {"apid": "c1"}, ["apid"], [], True),
        ("compute-only role is pending, not blocking",
         [("c1", ["pkg/apid/server.go", "deploy/ansible/roles/vmmd_service/files/faas-vmmd.service"])],
         {"apid": "c1"}, ["apid"], [], True),
        ("control-plane role next to a compute role still blocks",
         [("c1", ["deploy/ansible/roles/vmmd_service/x.yml", "deploy/ansible/roles/prometheus/x.yml"])],
         {}, [], ["deploy"], False),
        ("unit version is the newest commit that touched the unit",
         [("c3", ["pkg/sched/sched.go"]), ("c2", ["pkg/apid/server.go"]), ("c1", ["pkg/state/store.go"])],
         {"apid": "c2", "meterd": "c1", "schedd": "c3", "gatewayd-internal": "c1", "gregalectl": "c1"},
         ["apid", "gatewayd-internal", "meterd"], [], True),
        ("edge-only change deploys through the edge lane",
         [("c1", ["pkg/gateway/proxy.go"])], {"gatewayd-internal": "c1"}, ["gatewayd-internal"], [], True),
    ]

    def test_cases(self):
        for name, commits, want_changed, deployable, blocked, eligible in self.CASES:
            with self.subTest(name):
                rep = au.analyze(commits, CLOSURES, CONFIG, EXISTS)
                self.assertEqual(changed(rep), want_changed)
                self.assertEqual(rep["deployable"], deployable)
                self.assertEqual(rep["blocked_by"], blocked)
                self.assertEqual(rep["eligible"], eligible)

    def test_versions_are_stamped_from_the_unit_commit(self):
        rep = au.analyze([("c2", ["pkg/apid/server.go"]), ("c1", ["pkg/state/store.go"])], CLOSURES, CONFIG, EXISTS)
        self.assertEqual(rep["binaries"]["apid"]["version"], "unit-c2")
        self.assertEqual(rep["binaries"]["meterd"]["version"], "unit-c1")
        self.assertIsNone(rep["binaries"]["vmmd"]["version"])

    def test_pending_lists_units_left_at_base(self):
        rep = au.analyze([("c1", ["pkg/state/store.go", "pkg/fcvm/vm_linux.go"])], CLOSURES, CONFIG, EXISTS)
        self.assertEqual(rep["pending"], {"shared": ["schedd"], "compute": ["vmmd"], "tool": ["gregalectl"],
                                          "control-plane": [], "edge": []})

    def test_compute_only_role_blocks_the_edge_lane_only(self):
        # The edge lane lands on compute hosts without converging their
        # roles, so a compute role change waits for the full release; the
        # control-plane lane is unaffected.
        rep = au.analyze([("c1", ["pkg/state/store.go", "deploy/ansible/roles/vmmd_service/tasks/main.yml"])],
                         CLOSURES, CONFIG, EXISTS)
        self.assertTrue(rep["lanes"]["control-plane"]["eligible"])
        self.assertEqual(rep["lanes"]["edge"]["blocked_by"], ["compute_deploy"])
        self.assertFalse(rep["lanes"]["edge"]["eligible"])
        self.assertEqual(rep["deployable"], ["apid", "meterd"])
        self.assertEqual(rep["pending"]["edge"], ["gatewayd-internal"])
        self.assertTrue(rep["eligible"])

    def test_lanes_are_judged_separately(self):
        rep = au.analyze([("c1", ["pkg/gateway/proxy.go"])], CLOSURES, CONFIG, EXISTS)
        self.assertFalse(rep["lanes"]["control-plane"]["eligible"])
        self.assertIn("no control-plane daemon changed", rep["lanes"]["control-plane"]["reasons"][0])
        self.assertTrue(rep["lanes"]["edge"]["eligible"])

    def test_build_recipe_change_is_a_change_to_every_binary(self):
        rep = au.analyze([("c1", ["Makefile"])], CLOSURES, CONFIG, EXISTS, recipe_shas={"c1"})
        self.assertEqual(set(changed(rep)), set(CLOSURES))
        self.assertIn("build_recipe", rep["flags"])
        self.assertTrue(rep["eligible"])

    def test_makefile_change_outside_the_recipe_changes_nothing(self):
        rep = au.analyze([("c1", ["Makefile"])], CLOSURES, CONFIG, EXISTS)
        self.assertEqual(changed(rep), {})

    def test_reasons_explain_ineligibility(self):
        rep = au.analyze([("c1", ["migrations/00002_add.sql"])], CLOSURES, CONFIG, EXISTS)
        self.assertEqual(rep["reasons"], [
            "migrations changed since base (1 paths, e.g. migrations/00002_add.sql)",
            "no control-plane daemon changed since base",
            "migrations changed since base (1 paths, e.g. migrations/00002_add.sql)",
            "no edge daemon changed since base",
        ])

    def test_lane_table_and_loaded_binaries_must_agree(self):
        missing = dict(CLOSURES)
        del missing["vmmd"]
        with self.assertRaisesRegex(ValueError, "vmmd is in the lane table"):
            au.analyze([], missing, CONFIG, EXISTS)
        extra = dict(CLOSURES, newd=(set(), set()))
        with self.assertRaisesRegex(ValueError, "newd has no lane"):
            au.analyze([], extra, CONFIG, EXISTS)


class HelperTest(unittest.TestCase):
    def test_build_recipe_ignores_unrelated_targets(self):
        before = "GO ?= go\nDAEMONS := a b\n\ntest: ## run\n\tgo test ./...\n\nbuild-release-batch: ## x\n\tgo build -o x\n"
        after = before.replace("go test ./...", "go test -race ./...")
        self.assertEqual(au.build_recipe(before), au.build_recipe(after))
        flags = before.replace("go build -o x", "go build -trimpath -o x")
        self.assertNotEqual(au.build_recipe(before), au.build_recipe(flags))
        renamed = before.replace("DAEMONS := a b", "DAEMONS := a b c")
        self.assertNotEqual(au.build_recipe(before), au.build_recipe(renamed))

    def test_matches_prefix_and_exact_paths(self):
        self.assertTrue(au.matches("deploy/x.yml", ["deploy/"]))
        self.assertTrue(au.matches("docs/DPA.md", ["docs/DPA.md"]))
        self.assertFalse(au.matches("docs/DPA.md.bak", ["docs/DPA.md"]))
        self.assertFalse(au.matches("deployctl/x", ["deploy/"]))
        self.assertFalse(au.matches("deploy/roles/x_test.go", ["deploy/"]))

    def test_closure_follows_module_imports_only(self):
        graph = {
            "m/cmd/a": {"dir": "cmd/a", "imports": {"m/pkg/x", "fmt"}, "files": {"cmd/a/main.go"}},
            "m/pkg/x": {"dir": "pkg/x", "imports": {"m/pkg/y"}, "files": {"pkg/x/x.go"}},
            "m/pkg/y": {"dir": "pkg/y", "imports": set(), "files": {"pkg/y/y.go"}},
            "m/pkg/z": {"dir": "pkg/z", "imports": set(), "files": {"pkg/z/z.go"}},
        }
        dirs, files = au.closure("m/cmd/a", graph)
        self.assertEqual(dirs, {"cmd/a", "pkg/x", "pkg/y"})
        self.assertEqual(files, {"cmd/a/main.go", "pkg/x/x.go", "pkg/y/y.go"})


class RepositoryContractTest(unittest.TestCase):
    """The lane table must stay in lockstep with what the repo ships."""

    def setUp(self):
        self.config = au.load_config()
        self.makefile = (REPO / "Makefile").read_text(encoding="utf-8")

    def test_every_release_binary_has_a_lane(self):
        shipped = set(au.makefile_list(self.makefile, "DAEMONS")) | set(au.makefile_list(self.makefile, "CLIS"))
        self.assertEqual(shipped, set(self.config["binaries"]))

    def test_makefile_build_recipe_is_extracted(self):
        recipe = au.build_recipe(self.makefile)
        for needle in ("GO_BUILD_FLAGS", "LDFLAGS", "build-release-batch:", "build-release-unit:"):
            self.assertIn(needle, recipe)

    def test_control_plane_bundle_sources_are_covered(self):
        # Every repository file cd-controlplane copies into its bundle is
        # reused from the base release by the unit lane, so a change to one
        # must block that lane.
        workflow = (REPO / ".github/workflows/cd-controlplane.yml").read_text(encoding="utf-8")
        start = workflow.index("- name: Build immutable release bundle")
        end = workflow.index("- name:", start + 1)
        step = "\n".join(line for line in workflow[start:end].split("\n") if not line.lstrip().startswith("#"))
        sources = set(re.findall(r"(?<![\w/$.-])((?:deploy|docs|pkg|scripts)/[\w./-]+)", step))
        self.assertTrue(sources, "found no repository sources in the bundle step")
        blocking = [p for prefixes in self.config["blocking_paths"].values() for p in prefixes]
        for source in sorted(sources):
            with self.subTest(source):
                self.assertTrue(au.matches(source, blocking) or source.endswith("/"),
                                f"{source} is copied into the control-plane bundle but no blocking path covers it")

    def test_compute_only_roles_never_run_on_the_control_plane(self):
        # compute_only_paths lets a role change wait for the full release.
        # That is only sound for roles the control-plane play never runs,
        # directly or through another role: include_role/import_role
        # (name: X), meta dependencies (role: X) and file lookups
        # (roles/X/, ../X/) all name the role in one of these forms.
        bootstrap = (REPO / "deploy/ansible/bootstrap.yml").read_text(encoding="utf-8")
        plays = re.split(r"^- name: ", bootstrap, flags=re.M)[1:]
        control = [p for p in plays if re.search(r"^  hosts: .*\bcontrol_plane\b", p, re.M)]
        self.assertTrue(control, "bootstrap.yml has no control-plane play")
        cp_roles = {r for play in control for r in re.findall(r"^\s+- role: ([\w-]+)", play, re.M)}
        self.assertIn("control_plane_service", cp_roles)
        compute_only = {p.rstrip("/").rsplit("/", 1)[-1] for p in self.config["compute_only_paths"]}
        for role in sorted(compute_only):
            with self.subTest(role):
                self.assertTrue((REPO / "deploy/ansible/roles" / role).is_dir(), f"{role} is not a role")
                self.assertNotIn(role, cp_roles)
        for cp_role in sorted(cp_roles):
            root = REPO / "deploy/ansible/roles" / cp_role
            for path in root.rglob("*"):
                if not path.is_file() or path.suffix not in (".yml", ".yaml", ".j2"):
                    continue
                text = "\n".join(line for line in path.read_text(encoding="utf-8", errors="replace").split("\n")
                                 if not line.lstrip().startswith("#"))
                for role in compute_only:
                    with self.subTest(f"{cp_role} -> {role}"):
                        self.assertIsNone(re.search(rf"(?:role:\s*|name:\s*|roles/|\.\./){role}\b", text),
                                          f"{path.relative_to(REPO)} names compute-only role {role}")

    def test_deployable_lanes_name_services(self):
        for name, entry in self.config["binaries"].items():
            if entry["lane"] in au.DEPLOY_LANES:
                self.assertTrue(entry.get("services"), name)

    def test_edge_lane_is_the_gateways(self):
        # The edge lane rolls host by host without draining: only the
        # gateways hand traffic over that way. vmmd and compute schedd are
        # the vmmd session's (restart survival, ADR-20261018072701 and node drain).
        edge = sorted(n for n, e in self.config["binaries"].items() if e["lane"] == "edge")
        self.assertEqual(edge, ["gatewayd-internal", "gatewayd-public"])


@unittest.skipUnless(shutil.which(os.environ.get("GO", "go")), "go toolchain not available")
class EndToEndTest(unittest.TestCase):
    """report() on a throwaway module: real go list, real git history."""

    def run_git(self, *args):
        subprocess.run(["git", *args], cwd=self.root, check=True, capture_output=True)

    def write(self, path, body):
        full = os.path.join(self.root, path)
        os.makedirs(os.path.dirname(full), exist_ok=True)
        with open(full, "w", encoding="utf-8") as fh:
            fh.write(body)

    def commit(self, message):
        self.run_git("add", "-A")
        self.run_git("commit", "-q", "-m", message)
        return subprocess.run(["git", "rev-parse", "HEAD"], cwd=self.root, check=True,
                              capture_output=True, text=True).stdout.strip()

    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.root)
        os.environ.setdefault("GOTOOLCHAIN", "local")
        os.environ.setdefault("GOFLAGS", "")
        self.run_git("init", "-q", "-b", "main")
        self.run_git("config", "user.email", "t@example.com")
        self.run_git("config", "user.name", "t")
        self.write("go.mod", "module example.com/m\n\ngo 1.21\n")
        self.write("Makefile", "GO_BUILD_FLAGS := -trimpath\nDAEMONS := apid vmmd\nCLIS := gregalectl\n\n"
                               "build-release-batch:\n\tgo build $(GO_BUILD_FLAGS)\n\n"
                               "build-release-unit:\n\tgo build $(GO_BUILD_FLAGS)\n")
        self.write("cmd/apid/main.go", 'package main\n\nimport "example.com/m/pkg/api"\n\nfunc main() { api.Serve() }\n')
        self.write("cmd/vmmd/main.go", 'package main\n\nimport _ "example.com/m/pkg/fc"\n\nfunc main() {}\n')
        self.write("cmd/gregalectl/main.go", "package main\n\nfunc main() {}\n")
        self.write("pkg/api/api.go", "package api\n\nfunc Serve() {}\n")
        self.write("pkg/fc/fc.go", "package fc\n")
        # Only the metal build of vmmd reads this file.
        self.write("pkg/fc/metal.go", "//go:build metal\n\npackage fc\n")
        self.write("pkg/fc/other_darwin.go", "package fc\n")
        self.config = os.path.join(self.root, "lanes.json")
        with open(self.config, "w", encoding="utf-8") as fh:
            json.dump({"binaries": {"apid": {"lane": "control-plane", "services": [{"name": "apid"}]},
                                    "vmmd": {"lane": "compute"}, "gregalectl": {"lane": "tool"}},
                       "global_inputs": ["go.mod", "go.sum"],
                       "blocking_paths": {"migrations": ["migrations/"]}}, fh)
        self.base = self.commit("base")

    def test_report_selects_by_closure_and_build_tags(self):
        self.write("pkg/api/api.go", "package api\n\nfunc Serve() { _ = 1 }\n")
        apid = self.commit("apid change")
        self.write("pkg/fc/metal.go", "//go:build metal\n\npackage fc\n\nvar X = 1\n")
        vmmd = self.commit("metal-only change")
        self.write("pkg/fc/other_darwin.go", "package fc\n\nvar Y = 1\n")
        self.commit("darwin-only change")
        makefile = pathlib.Path(self.root, "Makefile")
        self.write("Makefile", makefile.read_text(encoding="utf-8") + "\nlint:\n\tgo vet ./...\n")
        self.commit("unrelated make target")
        rep = au.report(self.base, "HEAD", self.config, root=self.root)
        self.assertEqual(changed(rep), {"apid": apid, "vmmd": vmmd})
        self.assertEqual(rep["deployable"], ["apid"])
        self.assertTrue(rep["eligible"])

    def test_report_blocks_on_migrations_and_rejects_a_stale_checkout(self):
        self.write("pkg/api/api.go", "package api\n\nfunc Serve() { _ = 2 }\n")
        self.write("migrations/00002_x.sql", "select 1;\n")
        head = self.commit("apid + migration")
        rep = au.report(self.base, head, self.config, root=self.root)
        self.assertEqual(rep["blocked_by"], ["migrations"])
        self.assertFalse(rep["eligible"])
        self.run_git("checkout", "-q", self.base)
        with self.assertRaisesRegex(RuntimeError, "not --head"):
            au.report(self.base, head, self.config, root=self.root)


if __name__ == "__main__":
    unittest.main()
