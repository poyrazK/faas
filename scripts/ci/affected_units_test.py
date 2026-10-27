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
# Throwaway repositories must not start git's detached auto-maintenance:
# it writes maintenance.lock and pack bitmaps while tearDown deletes the
# tree, which fails the test with FileNotFoundError.
GIT = ("git", "-c", "maintenance.auto=false", "-c", "gc.auto=0")
spec = importlib.util.spec_from_file_location("affected_units", HERE / "affected_units.py")
au = importlib.util.module_from_spec(spec)
spec.loader.exec_module(au)
host_roles = au.host_roles

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
    "host_config": {
        "converged_groups": ["control_plane"],
        "release_units": ["deploy/ansible/roles/control_plane_service/files/faas-apid.service"],
    },
    "deploy_classes": {
        "inert": ["deploy/**/*.md", "deploy/**/*.example", "deploy/**/*.example.*", "deploy/systemd/", "deploy/grafana/"],
        "compute_only": ["deploy/ansible/node_join.yml"],
        "blocking": ["deploy/ansible/site.yml", "deploy/manifest/production/"],
    },
}

# Paths each host group's roles read (host_roles.group_owners); "*" is a
# group global, read by every role.
HOST = {
    "control_plane": {"owners": {
        "deploy/ansible/bootstrap.yml": ["*"],
        "deploy/ansible/roles/prometheus/x.yml": ["prometheus"],
        "deploy/ansible/roles/postgres_capacity/defaults/main.yml": ["postgres_capacity"],
        "deploy/ansible/roles/host_hardening/tasks/main.yml": ["host_hardening"],
        "deploy/ansible/roles/control_plane_service/files/faas-apid.service": ["control_plane_service"],
        "deploy/systemd/faas-pg-basebackup.service": ["postgres_backup"],
        "deploy/ansible/roles/grafana/files/fleet.json": ["grafana"],
    }, "restart_roles": ["postgres", "postgres_capacity"]},
    "compute_nodes": {"owners": {
        "deploy/ansible/bootstrap.yml": ["*"],
        "deploy/ansible/roles/vmmd_service/files/faas-vmmd.service": ["vmmd_service"],
        "deploy/ansible/roles/vmmd_service/tasks/main.yml": ["vmmd_service"],
        "deploy/ansible/roles/vmmd_service/x.yml": ["vmmd_service"],
        "deploy/ansible/roles/host_hardening/tasks/main.yml": ["host_hardening"],
    }},
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
        ("control-plane role change rides the control-plane lane",
         [("c1", ["deploy/ansible/roles/vmmd_service/x.yml", "deploy/ansible/roles/prometheus/x.yml"])],
         {}, [], [], True),
        ("control-plane role file change rides the lane with a daemon",
         [("c2", ["pkg/apid/server.go"]), ("c1", ["deploy/ansible/roles/grafana/files/fleet.json"])],
         {"apid": "c2"}, ["apid"], [], True),
        ("a role that can restart PostgreSQL blocks",
         [("c2", ["pkg/apid/server.go"]), ("c1", ["deploy/ansible/roles/postgres_capacity/defaults/main.yml"])],
         {"apid": "c2"}, [], ["stateful_restart"], False),
        ("unit tree file a control-plane role reads is host config",
         [("c1", ["pkg/apid/server.go", "deploy/systemd/faas-pg-basebackup.service"])],
         {"apid": "c1"}, ["apid"], [], True),
        ("legacy unit tree file no role reads maps to nothing",
         [("c1", ["pkg/apid/server.go", "deploy/systemd/faas-gatewayd-internal.service"])],
         {"apid": "c1"}, ["apid"], [], True),
        ("deploy documentation maps to nothing",
         [("c1", ["pkg/apid/server.go", "deploy/ansible/roles/prometheus/README.md", "deploy/README.md"])],
         {"apid": "c1"}, ["apid"], [], True),
        ("dashboard mirror maps to nothing", [("c1", ["pkg/apid/server.go", "deploy/grafana/fleet.json"])],
         {"apid": "c1"}, ["apid"], [], True),
        ("release unit file blocks",
         [("c1", ["pkg/apid/server.go", "deploy/ansible/roles/control_plane_service/files/faas-apid.service"])],
         {"apid": "c1"}, [], ["release_units"], False),
        ("deploy path no host role reads blocks",
         [("c1", ["pkg/apid/server.go", "deploy/manifest/production/gcp-live.template.yaml"])],
         {"apid": "c1"}, [], ["deploy"], False),
        ("deploy path no rule names blocks",
         [("c1", ["pkg/apid/server.go", "deploy/new-tree/run.sh"])], {"apid": "c1"}, [], ["deploy"], False),
        ("sample no role reads maps to nothing",
         [("c1", ["pkg/apid/server.go", "deploy/managed-postgres.example.json", "deploy/etc/apid.toml.example"])],
         {"apid": "c1"}, ["apid"], [], True),
        ("compute playbook is pending for the control-plane lane",
         [("c1", ["pkg/apid/server.go", "deploy/ansible/node_join.yml"])], {"apid": "c1"}, ["apid"], [], True),
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
                rep = au.analyze(commits, CLOSURES, CONFIG, EXISTS, host=HOST)
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
                         CLOSURES, CONFIG, EXISTS, host=HOST)
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


def measured(status, code="h", base="b"):
    """A measure_code entry: status against base and the two code digests."""
    return {"code": status, "code_digest": code * 64, "base_code_digest": base * 64 if base else None}


class CodeSelectionTest(unittest.TestCase):
    """analyze() with code measurements (measure_code)."""

    STATE = [("c1", ["pkg/state/store.go"])]

    def test_closure_binary_with_unchanged_code_is_carried_not_changed(self):
        code = {"apid": measured("same", "a", "a"), "meterd": measured("changed"),
                "gatewayd-internal": measured("lines", "g", "g")}
        rep = au.analyze(self.STATE, CLOSURES, CONFIG, EXISTS, code=code)
        apid = rep["binaries"]["apid"]
        self.assertEqual((apid["closure"], apid["changed"], apid["code"]), (True, False, "same"))
        self.assertEqual(apid["version"], "unit-c1")
        self.assertEqual(apid["code_digest"], apid["base_code_digest"])
        self.assertTrue(rep["binaries"]["meterd"]["changed"])
        self.assertFalse(rep["binaries"]["gatewayd-internal"]["changed"])
        self.assertEqual(rep["lanes"]["control-plane"]["changed"], ["meterd"])
        self.assertEqual(rep["lanes"]["control-plane"]["carried"], ["apid", "meterd"])
        # Every closure binary of an eligible lane ships in the tarball, so a
        # host on an earlier unit release can get back to base's code.
        self.assertEqual(rep["deployable"], ["apid", "gatewayd-internal", "meterd"])
        self.assertTrue(rep["lanes"]["edge"]["eligible"])
        self.assertEqual(rep["lanes"]["edge"]["changed"], [])

    def test_unmeasured_binaries_keep_the_closure(self):
        rep = au.analyze(self.STATE, CLOSURES, CONFIG, EXISTS, code={"apid": measured("same")})
        self.assertTrue(rep["binaries"]["schedd"]["changed"])
        self.assertIsNone(rep["binaries"]["schedd"]["code"])
        self.assertEqual(rep["pending"]["shared"], ["schedd"])
        self.assertTrue(rep["binaries"]["meterd"]["changed"])

    def test_added_binary_is_changed(self):
        rep = au.analyze(self.STATE, CLOSURES, CONFIG, EXISTS, code={"apid": measured("added", base=None)})
        self.assertTrue(rep["binaries"]["apid"]["changed"])
        self.assertIsNone(rep["binaries"]["apid"]["base_code_digest"])

    def test_measurement_of_a_binary_outside_the_closure_is_ignored(self):
        rep = au.analyze([("c1", ["pkg/apid/server.go"])], CLOSURES, CONFIG, EXISTS,
                         code={"meterd": measured("changed")})
        self.assertFalse(rep["binaries"]["meterd"]["changed"])
        self.assertIsNone(rep["binaries"]["meterd"]["code"])

    def test_candidates_skip_blocked_lanes_and_lanes_that_never_deploy(self):
        rep = au.analyze([("c1", ["pkg/state/store.go", "deploy/ansible/roles/vmmd_service/tasks/main.yml"])],
                         CLOSURES, CONFIG, EXISTS, host=HOST)
        self.assertEqual(au.code_candidates(rep), ["apid", "meterd"])
        rep = au.analyze([("c1", ["pkg/state/store.go", "migrations/00002_add.sql"])], CLOSURES, CONFIG, EXISTS)
        self.assertEqual(au.code_candidates(rep), [])

    def test_code_cache_round_trip_and_mismatch(self):
        path = os.path.join(tempfile.mkdtemp(), "cache.json")
        self.addCleanup(shutil.rmtree, os.path.dirname(path))
        self.assertEqual(au.load_code_cache(path, "b" * 40, "go1.26.9"), {})
        au.write_code_cache(path, "b" * 40, "go1.26.9", {"apid": ("f" * 64, "c" * 64)})
        self.assertEqual(au.load_code_cache(path, "b" * 40, "go1.26.9"), {"apid": ("f" * 64, "c" * 64)})
        self.assertEqual(au.load_code_cache(path, "a" * 40, "go1.26.9"), {}, "another base")
        self.assertEqual(au.load_code_cache(path, "b" * 40, "go1.27.0"), {}, "another toolchain")
        with open(path, "w", encoding="utf-8") as fh:
            fh.write("{not json")
        self.assertEqual(au.load_code_cache(path, "b" * 40, "go1.26.9"), {})


class HostConfigTest(unittest.TestCase):
    """A change to a control-plane host role rides the control-plane lane,
    which converges the role before it restarts anything
    (docs/adr/20261018133100-host-configuration-lane.md)."""

    def test_role_change_alone_deploys_the_control_plane_lane(self):
        rep = au.analyze([("c2", ["docs/quickstart.md"]),
                          ("c1", ["deploy/ansible/roles/grafana/files/fleet.json"])],
                         CLOSURES, CONFIG, EXISTS, host=HOST)
        self.assertEqual(rep["host_config"], {"group": "control_plane", "roles": ["grafana"], "all": False,
                                              "commit": "c1", "version": "host-c1"})
        self.assertEqual(rep["flags"]["host_config"],
                         {"blocking": False, "count": 1, "paths": ["deploy/ansible/roles/grafana/files/fleet.json"]})
        self.assertTrue(rep["lanes"]["control-plane"]["eligible"])
        self.assertEqual(rep["lanes"]["control-plane"]["reasons"], [])
        self.assertFalse(rep["lanes"]["edge"]["eligible"])
        self.assertEqual(rep["deployable"], [])
        self.assertTrue(rep["eligible"])

    def test_role_both_planes_run_holds_the_edge_lane(self):
        # host_hardening runs on compute hosts too, which no lane converges
        # yet: the edge lane waits for the full release.
        rep = au.analyze([("c1", ["pkg/state/store.go", "deploy/ansible/roles/host_hardening/tasks/main.yml"])],
                         CLOSURES, CONFIG, EXISTS, host=HOST)
        self.assertEqual(rep["host_config"]["roles"], ["host_hardening"])
        self.assertTrue(rep["lanes"]["control-plane"]["eligible"])
        self.assertEqual(rep["lanes"]["edge"]["blocked_by"], ["compute_deploy"])

    def test_content_hashes_decide_the_roles(self):
        # A path changed and changed back: the owners say postgres_capacity,
        # the hashes say nothing changed, so there is nothing to converge.
        host = dict(HOST, control_plane=dict(HOST["control_plane"], changed={"all": False, "roles": []}))
        rep = au.analyze([("c1", ["pkg/apid/server.go", "deploy/ansible/roles/postgres_capacity/defaults/main.yml"])],
                         CLOSURES, CONFIG, EXISTS, host=host)
        self.assertEqual(rep["host_config"]["roles"], [])
        self.assertIsNone(rep["host_config"]["version"])
        self.assertTrue(rep["lanes"]["control-plane"]["eligible"])
        rep = au.analyze([("c1", ["deploy/ansible/roles/postgres_capacity/defaults/main.yml"])],
                         CLOSURES, CONFIG, EXISTS, host=host)
        self.assertFalse(rep["lanes"]["control-plane"]["eligible"])
        self.assertIn("no control-plane daemon changed since base", rep["lanes"]["control-plane"]["reasons"])

    def test_play_change_converges_every_role_so_it_blocks(self):
        # Every role includes the ones that can restart PostgreSQL.
        rep = au.analyze([("c1", ["deploy/ansible/bootstrap.yml"])], CLOSURES, CONFIG, EXISTS, host=HOST)
        self.assertTrue(rep["host_config"]["all"])
        self.assertEqual(rep["flags"]["stateful_restart"]["paths"], ["deploy/ansible/bootstrap.yml"])
        self.assertEqual(rep["flags"]["compute_deploy"]["paths"], ["deploy/ansible/bootstrap.yml"])
        self.assertFalse(rep["lanes"]["control-plane"]["eligible"])
        self.assertFalse(rep["lanes"]["edge"]["eligible"])

    def test_postgres_restart_roles_block_and_name_their_paths(self):
        rep = au.analyze([("c2", ["deploy/ansible/roles/grafana/files/fleet.json"]),
                          ("c1", ["deploy/ansible/roles/postgres_capacity/defaults/main.yml"])],
                         CLOSURES, CONFIG, EXISTS, host=HOST)
        self.assertEqual(rep["flags"]["stateful_restart"],
                         {"blocking": True, "count": 1, "paths": ["deploy/ansible/roles/postgres_capacity/defaults/main.yml"]})
        self.assertEqual(rep["host_config"]["roles"], ["grafana", "postgres_capacity"])
        self.assertIn("stateful_restart changed since base (1 paths, e.g. deploy/ansible/roles/postgres_capacity/defaults/main.yml)",
                      rep["lanes"]["control-plane"]["reasons"])

    def test_without_a_host_view_deploy_blocks(self):
        rep = au.analyze([("c1", ["pkg/apid/server.go", "deploy/ansible/roles/prometheus/x.yml"])],
                         CLOSURES, CONFIG, EXISTS)
        self.assertEqual(rep["blocked_by"], ["deploy"])
        self.assertIsNone(rep["host_config"]["version"])

    def test_bundle_file_a_role_installs_is_host_config(self):
        # docs/DPA.md is in the control-plane bundle; control_plane_service
        # installs it too, so converging the role applies it.
        host = dict(HOST, control_plane={"owners": dict(HOST["control_plane"]["owners"],
                                                        **{"docs/DPA.md": ["control_plane_service"]})})
        rep = au.analyze([("c1", ["docs/DPA.md"])], CLOSURES, CONFIG, EXISTS, host=host)
        self.assertEqual(rep["blocked_by"], [])
        self.assertEqual(rep["host_config"]["roles"], ["control_plane_service"])
        self.assertTrue(rep["lanes"]["control-plane"]["eligible"])
        rep = au.analyze([("c1", ["docs/DPA.md"])], CLOSURES, CONFIG, EXISTS, host=HOST)
        self.assertEqual(rep["blocked_by"], ["control_plane_bundle"])

    def test_compute_role_sample_is_still_role_content(self):
        # A role installs its sample beside the real config; the role's
        # class decides, as for any file the role reads.
        host = dict(HOST, compute_nodes={"owners": dict(
            HOST["compute_nodes"]["owners"], **{"deploy/ansible/roles/vmmd_service/files/vmmd.toml.example": ["vmmd_service"]})})
        rep = au.analyze([("c1", ["deploy/ansible/roles/vmmd_service/files/vmmd.toml.example"])],
                         CLOSURES, CONFIG, EXISTS, host=host)
        self.assertEqual(rep["flags"]["compute_deploy"]["paths"], ["deploy/ansible/roles/vmmd_service/files/vmmd.toml.example"])
        self.assertEqual(rep["deploy_classes"]["host_roles"]["count"], 1)

    def test_release_unit_blocks_although_a_role_reads_it(self):
        # deployctl reinstalls the base release's unit files at every unit
        # activation, so converging the role would not stick.
        rep = au.analyze([("c1", ["deploy/ansible/roles/control_plane_service/files/faas-apid.service"])],
                         CLOSURES, CONFIG, EXISTS, host=HOST)
        self.assertEqual(rep["blocked_by"], ["release_units"])
        self.assertNotIn("host_config", rep["flags"])
        self.assertFalse(rep["eligible"])


class DeployClassTest(unittest.TestCase):
    """Every deploy/ path has one class (deploy_class; docs/adr/
    20261027151755-unit-lane-deploy-path-classes.md)."""

    def test_classes_in_match_order(self):
        cases = [
            ("deploy/ansible/roles/control_plane_service/files/faas-apid.service", "release_units", ["release_units"]),
            ("deploy/ansible/roles/grafana/files/fleet.json", "host_roles", ["host_config"]),
            ("deploy/ansible/roles/host_hardening/tasks/main.yml", "host_roles", ["compute_deploy", "host_config"]),
            ("deploy/ansible/roles/vmmd_service/tasks/main.yml", "host_roles", ["compute_deploy"]),
            # A role reads it, so the inert unit tree does not decide.
            ("deploy/systemd/faas-pg-basebackup.service", "host_roles", ["host_config"]),
            ("deploy/systemd/faas-apid.service", "inert", []),
            ("deploy/grafana/frozen-tier.json", "inert", []),
            ("deploy/ansible/roles/seal_domains/README.md", "inert", []),
            ("deploy/manifest/README.md", "inert", []),
            ("deploy/claims/compute-node.example.yaml", "inert", []),
            ("deploy/ansible/node_join.yml", "compute_only", ["compute_deploy"]),
            ("deploy/manifest/production/gcp-live.template.yaml", "blocking", ["deploy"]),
            ("deploy/unknown/thing.yml", "unclassified", ["deploy"]),
        ]
        for path, cls, flags in cases:
            with self.subTest(path):
                self.assertEqual(au.deploy_class(path, CONFIG, HOST), (cls, flags))

    def test_comment_only_change_maps_to_nothing_unless_a_role_or_activation_reads_it(self):
        template = "deploy/manifest/production/gcp-live.template.yaml"
        unit = "deploy/ansible/roles/control_plane_service/files/faas-apid.service"
        role = "deploy/ansible/roles/vmmd_service/tasks/main.yml"
        comments = {template, unit, role, "deploy/ansible/node_join.yml"}
        self.assertEqual(au.deploy_class(template, CONFIG, HOST, comments), ("comments", []))
        self.assertEqual(au.deploy_class("deploy/ansible/node_join.yml", CONFIG, HOST, comments), ("comments", []))
        # A rendered role file and a unit file carry their comments onto the host.
        self.assertEqual(au.deploy_class(unit, CONFIG, HOST, comments), ("release_units", ["release_units"]))
        self.assertEqual(au.deploy_class(role, CONFIG, HOST, comments), ("host_roles", ["compute_deploy"]))
        rep = au.analyze([("c1", ["pkg/apid/server.go", template])], CLOSURES, CONFIG, EXISTS, host=HOST,
                         comment_only={template})
        self.assertTrue(rep["lanes"]["control-plane"]["eligible"])
        self.assertEqual(rep["deploy_classes"], {"comments": {"count": 1, "paths": [template]}})

    def test_compute_path_holds_the_edge_lane_only_while_it_restarts_a_compute_gateway(self):
        commits = [("c1", ["pkg/gateway/proxy.go", "deploy/ansible/node_join.yml"])]
        rep = au.analyze(commits, CLOSURES, CONFIG, EXISTS, host=HOST)
        self.assertEqual(rep["lanes"]["edge"]["blocked_by"], ["compute_deploy"])
        self.assertEqual(rep["pending"]["edge"], ["gatewayd-internal"])
        # The same gateway running only on the control plane lands there
        # alone; no compute host restarts, so nothing waits.
        binaries = dict(CONFIG["binaries"], **{"gatewayd-internal": {
            "lane": "edge", "services": [{"name": "gatewayd-internal", "host": "control-plane"}]}})
        rep = au.analyze(commits, CLOSURES, dict(CONFIG, binaries=binaries), EXISTS, host=HOST)
        self.assertEqual(rep["lanes"]["edge"]["blocked_by"], [])
        self.assertTrue(rep["lanes"]["edge"]["eligible"])
        self.assertIn("compute_deploy", rep["flags"])

    def test_unknown_class_is_rejected(self):
        path = os.path.join(tempfile.mkdtemp(), "lanes.json")
        self.addCleanup(shutil.rmtree, os.path.dirname(path))
        with open(path, "w", encoding="utf-8") as fh:
            json.dump(dict(CONFIG, deploy_classes={"ignored": ["deploy/x"]}), fh)
        with self.assertRaisesRegex(ValueError, "unknown class 'ignored'"):
            au.load_config(path)


class StatefulRestartHistoryTest(unittest.TestCase):
    """The real postgres_capacity and grafana roles in a throwaway history,
    classified through host_view (no Go toolchain needed)."""

    def run_git(self, *args):
        return subprocess.run([*GIT, *args], cwd=self.root, check=True, capture_output=True, text=True).stdout.strip()

    def commit(self):
        self.run_git("add", "-A")
        self.run_git("-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "x")
        return self.run_git("rev-parse", "HEAD")

    def setUp(self):
        self.root = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.root)
        self.run_git("init", "-q")
        for role in ("postgres_capacity", "grafana"):
            shutil.copytree(REPO / "deploy/ansible/roles" / role, pathlib.Path(self.root, "deploy/ansible/roles", role))
        pathlib.Path(self.root, "deploy/ansible/bootstrap.yml").write_text(
            "- name: cp\n  hosts: control_plane\n  roles:\n    - role: grafana\n    - role: postgres_capacity\n",
            encoding="utf-8")
        self.base = self.commit()

    def report(self):
        head = self.commit()
        host = au.host_view(self.base, head, self.root, CONFIG_WITH_STATEFUL["host_config"]["stateful_services"])
        commits = au.range_commits(self.base, head, self.root)
        return au.analyze(commits, CLOSURES, CONFIG_WITH_STATEFUL, EXISTS, host=host)

    def test_postgres_capacity_change_moving_max_connections_blocks_the_lane(self):
        defaults = pathlib.Path(self.root, "deploy/ansible/roles/postgres_capacity/defaults/main.yml")
        text = defaults.read_text(encoding="utf-8")
        budget = re.search(r"(?m)^faas_postgres_control_plane_pool_budget: (\d+)$", text)
        self.assertIsNotNone(budget, "postgres_capacity no longer declares the control-plane pool budget")
        # 40 more pooled connections moves the derived max_connections on
        # any fleet size, and only a postmaster restart applies it.
        defaults.write_text(text.replace(budget.group(0), f"faas_postgres_control_plane_pool_budget: {int(budget.group(1)) + 40}"),
                            encoding="utf-8")
        rep = self.report()
        self.assertEqual(rep["host_config"]["roles"], ["postgres_capacity"])
        self.assertEqual(rep["blocked_by"], ["stateful_restart"])
        self.assertEqual(rep["flags"]["stateful_restart"]["paths"],
                         ["deploy/ansible/roles/postgres_capacity/defaults/main.yml"])
        self.assertFalse(rep["lanes"]["control-plane"]["eligible"])
        self.assertFalse(rep["eligible"])

    def test_a_role_that_never_restarts_postgres_still_rides_the_lane(self):
        tasks = pathlib.Path(self.root, "deploy/ansible/roles/grafana/tasks/main.yml")
        tasks.write_text(tasks.read_text(encoding="utf-8") + "\n- name: grafana — noop\n  ansible.builtin.debug: {}\n",
                         encoding="utf-8")
        rep = self.report()
        self.assertEqual(rep["host_config"]["roles"], ["grafana"])
        self.assertEqual(rep["blocked_by"], [])
        self.assertTrue(rep["lanes"]["control-plane"]["eligible"])


CONFIG_WITH_STATEFUL = dict(CONFIG, host_config=dict(CONFIG["host_config"], stateful_services=["postgresql", "pgbouncer"]))
DATA_CONFIG = dict(CONFIG, migrations_dir="migrations/")
EXPAND = {"eligible": True, "blockers": [], "migrations": [
    {"path": "migrations/20261018000000001_a.sql", "version": 20261018000000001, "eligible": True, "blockers": []}]}
CONTRACT = {"eligible": False, "blockers": ["migrations/20261018000000001_a.sql: contract change drop-column apps.b at line 4"],
            "migrations": [{"path": "migrations/20261018000000001_a.sql", "eligible": False}]}
MIGRATION = "migrations/20261018000000001_a.sql"


class DataStepTest(unittest.TestCase):
    """Lane-eligible migrations ride the control-plane lane instead of
    closing the range (docs/adr/20261018084150-unit-lane-expand-migrations.md)."""

    CASES = [
        # name, commits (newest first), verdict, blocked_by, control-plane eligible, edge eligible, data version
        ("expand migration with daemons", [("c2", ["pkg/state/store.go"]), ("c1", [MIGRATION])], EXPAND,
         [], True, True, "data-c1"),
        ("contract migration closes both lanes", [("c2", ["pkg/state/store.go"]), ("c1", [MIGRATION])], CONTRACT,
         ["migrations"], False, False, None),
        ("unclassified migrations block", [("c1", ["pkg/state/store.go", MIGRATION])], None,
         ["migrations"], False, False, None),
        ("migration runner code still blocks", [("c1", ["pkg/state/store.go", MIGRATION, "migrations/embed.go"])], EXPAND,
         ["migrations"], False, False, "data-c1"),
        ("migration tests are not migrations", [("c1", ["pkg/apid/server.go", "migrations/20261018000000001_a_test.go"])],
         None, [], True, False, None),
        ("data alone deploys the control-plane lane", [("c1", [MIGRATION])], EXPAND, [], True, False, "data-c1"),
        ("deploy tree still blocks", [("c1", [MIGRATION, "deploy/ansible/site.yml", "pkg/state/store.go"])], EXPAND,
         ["deploy"], False, False, "data-c1"),
    ]

    def test_cases(self):
        for name, commits, verdict, blocked, cp, edge, version in self.CASES:
            with self.subTest(name):
                rep = au.analyze(commits, CLOSURES, DATA_CONFIG, EXISTS, data=verdict)
                self.assertEqual(rep["blocked_by"], blocked)
                self.assertEqual(rep["lanes"]["control-plane"]["eligible"], cp)
                self.assertEqual(rep["lanes"]["edge"]["eligible"], edge)
                self.assertEqual(rep["data"]["version"], version)
                # Whatever closes the control-plane lane closes the edge
                # lane: data always goes first.
                if rep["data"]["version"] and edge:
                    self.assertTrue(cp)

    def test_data_flag_and_version(self):
        rep = au.analyze([("c3", ["pkg/apid/server.go"]), ("c2", [MIGRATION]), ("c1", ["pkg/state/store.go"])],
                         CLOSURES, DATA_CONFIG, EXISTS, data=EXPAND)
        self.assertEqual(rep["flags"]["data"], {"blocking": False, "count": 1, "paths": [MIGRATION]})
        self.assertNotIn("migrations", rep["flags"])
        # Migrations embed into every daemon, so in practice they change
        # too; here only the data version follows the migration commit.
        self.assertEqual(rep["data"]["commit"], "c2")
        self.assertEqual(rep["data"]["migrations"], EXPAND["migrations"])
        self.assertEqual(rep["binaries"]["apid"]["commit"], "c3")

    def test_contract_reason_names_the_migration(self):
        rep = au.analyze([("c1", ["pkg/state/store.go", MIGRATION])], CLOSURES, DATA_CONFIG, EXISTS, data=CONTRACT)
        self.assertIn("migrations are not lane-eligible: migrations/20261018000000001_a.sql: contract change "
                      "drop-column apps.b at line 4", rep["reasons"])
        self.assertEqual(rep["flags"]["migrations"]["paths"], [MIGRATION])
        self.assertNotIn("data", rep["flags"])

    def test_data_only_range_is_eligible_with_no_units(self):
        rep = au.analyze([("c1", [MIGRATION])], CLOSURES, DATA_CONFIG, EXISTS, data=EXPAND)
        self.assertTrue(rep["eligible"])
        self.assertEqual(rep["deployable"], [])
        self.assertEqual(rep["lanes"]["control-plane"]["reasons"], [])


class HelperTest(unittest.TestCase):
    def test_build_recipe_ignores_unrelated_targets(self):
        before = "GO ?= go\nDAEMONS := a b\n\ntest: ## run\n\tgo test ./...\n\nbuild-release-batch: ## x\n\tgo build -o x\n"
        after = before.replace("go test ./...", "go test -race ./...")
        self.assertEqual(au.build_recipe(before), au.build_recipe(after))
        flags = before.replace("go build -o x", "go build -trimpath -o x")
        self.assertNotEqual(au.build_recipe(before), au.build_recipe(flags))
        renamed = before.replace("DAEMONS := a b", "DAEMONS := a b c")
        self.assertNotEqual(au.build_recipe(before), au.build_recipe(renamed))
        tagged = before.replace("DAEMONS := a b", "DAEMONS := a b\nDAEMON_TAGS := metal,grpcnotrace")
        self.assertNotEqual(au.build_recipe(before), au.build_recipe(tagged))

    def test_daemon_tags(self):
        self.assertEqual(au.daemon_tags("DAEMONS := a\n"), ["metal"])
        self.assertEqual(au.daemon_tags("DAEMON_TAGS := metal,grpcnotrace\n"), ["metal", "grpcnotrace"])
        makefile = (REPO / "Makefile").read_text(encoding="utf-8")
        self.assertIn("grpcnotrace", au.daemon_tags(makefile))
        self.assertNotIn("-tags metal", au.build_recipe(makefile))

    def test_glob_match(self):
        cases = [
            ("deploy/systemd/", "deploy/systemd/faas-apid.service", True),
            ("deploy/systemd/", "deploy/systemd-old/x", False),
            ("deploy/ansible/site.yml", "deploy/ansible/site.yml", True),
            ("deploy/ansible/site.yml", "deploy/ansible/site.yml.bak", False),
            ("deploy/**/*.md", "deploy/README.md", True),
            ("deploy/**/*.md", "deploy/ansible/roles/x/README.md", True),
            ("deploy/**/*.example", "deploy/etc/apid.toml.example", True),
            ("deploy/**/*.example.*", "deploy/managed-postgres.example.json", True),
            ("deploy/**/*.example", "deploy/managed-postgres.example.json", False),
            ("deploy/**/tests/**", "deploy/ansible/tests/obs_stack_smoke.yml", True),
            ("deploy/*.json", "deploy/a/b.json", False),
            ("deploy/**/*_test.sh", "deploy/scripts/leakcheck_test.sh", True),
        ]
        for pattern, path, want in cases:
            with self.subTest(pattern=pattern, path=path):
                self.assertEqual(au.glob_match(path, pattern), want)

    def test_comment_free_keeps_content_and_shebangs(self):
        self.assertEqual(au.comment_free("#!/bin/bash\n# note\n\n  # indented\nset -e  # trailing\n"),
                         ["#!/bin/bash", "set -e  # trailing"])

    def test_comment_only_paths(self):
        root = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, root)
        run = lambda *a: subprocess.run([*GIT, *a], cwd=root, check=True, capture_output=True, text=True).stdout
        run("init", "-q", "-b", "main")
        run("config", "user.email", "t@example.com")
        run("config", "user.name", "t")
        files = {"deploy/a.yaml": "a: 1\n", "deploy/b.yml": "b: 1\n", "deploy/c.json": "{}\n",
                 "deploy/d.sh": "#!/bin/sh\ntrue\n"}
        for path, body in files.items():
            os.makedirs(os.path.join(root, os.path.dirname(path)), exist_ok=True)
            pathlib.Path(root, path).write_text(body)
        run("add", "-A")
        run("commit", "-q", "-m", "base")
        base = run("rev-parse", "HEAD").strip()
        pathlib.Path(root, "deploy/a.yaml").write_text("# workload identity, off for now\n#  key: x\n\na: 1\n")
        pathlib.Path(root, "deploy/b.yml").write_text("b: 2  # was 1\n")
        pathlib.Path(root, "deploy/c.json").write_text('{"x": 1}\n')
        pathlib.Path(root, "deploy/d.sh").write_text("#!/bin/bash\ntrue\n")
        pathlib.Path(root, "deploy/new.yml").write_text("# only comments\n")
        run("add", "-A")
        run("commit", "-q", "-m", "head")
        paths = set(files) | {"deploy/new.yml"}
        self.assertEqual(au.comment_only_paths(base, "HEAD", paths, root), {"deploy/a.yaml"})

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

    def bundle_step(self):
        workflow = (REPO / ".github/workflows/cd-controlplane.yml").read_text(encoding="utf-8")
        start = workflow.index("- name: Build immutable release bundle")
        end = workflow.index("- name:", start + 1)
        return "\n".join(line for line in workflow[start:end].split("\n") if not line.lstrip().startswith("#"))

    def test_control_plane_bundle_sources_are_covered(self):
        # Every repository file cd-controlplane copies into its bundle is
        # reused from the base release by the unit lane. A change to one
        # must block that lane, or be a release unit, or be read by a
        # control-plane host role, which the lane converges first.
        step = self.bundle_step()
        sources = set(re.findall(r"(?<![\w/$.-])((?:deploy|docs|pkg|scripts)/[\w./-]+)", step))
        self.assertTrue(sources, "found no repository sources in the bundle step")
        blocking = [p for flag, prefixes in self.config["blocking_paths"].items() if flag != "deploy" for p in prefixes]
        owners = host_roles.group_owners(host_roles.fs_tree(str(REPO)), "control_plane")
        for source in sorted(sources):
            with self.subTest(source):
                # Directories are the unit loop's role trees; the release
                # units test covers what the loop takes from them.
                self.assertTrue(au.matches(source, blocking) or (REPO / source).is_dir()
                                or source in self.config["host_config"]["release_units"] or source in owners,
                                f"{source} is copied into the control-plane bundle but neither blocks nor is host config")

    def test_release_units_are_what_activation_installs(self):
        # deployctl installs the bundle's systemd/, optional-systemd/ and
        # tmpfiles.d/ at every activation, a unit release's included.
        step = self.bundle_step()
        units = re.search(r"for unit in \\\n(.*?); do", step, re.S).group(1).split()
        role_dirs = re.search(r"for role_dir in \\\n(.*?); do", step, re.S).group(1).split()
        want = set()
        for unit in (u.strip("\\") for u in units if u != "\\"):
            found = [f"{d}/{unit}" for d in role_dirs if d != "\\" and (REPO / d / unit).is_file()]
            self.assertTrue(found, f"{unit} is in no role files/ tree")
            want.add(found[0])
        for dest in ("optional-systemd", "tmpfiles.d"):
            want.update(re.findall(rf"((?:deploy)/[\w./-]+)\s*\\?\s*\n?\s*\"\$\{{BUNDLE_ROOT\}}/{dest}/", step))
        self.assertEqual(sorted(want), sorted(self.config["host_config"]["release_units"]))

    def host(self):
        tree = host_roles.fs_tree(str(REPO))
        roles = host_roles.Roles(tree)
        return {group: {"owners": host_roles.group_owners(tree, group, roles)} for group in host_roles.GROUPS}

    def deploy_paths(self):
        out = subprocess.run(["git", "ls-files", "-z", "deploy"], cwd=REPO, check=True, capture_output=True, text=True)
        return sorted(p for p in out.stdout.split("\0") if p)

    def test_every_deploy_path_has_exactly_one_class(self):
        # The table names every deploy/ path the repository has, so a new
        # tree is classified on purpose rather than blocking both lanes by
        # default; and every pattern still decides some path.
        host, classes = self.host(), self.config["deploy_classes"]
        deciding = set()
        paths = self.deploy_paths()
        self.assertGreater(len(paths), 500)
        for path in paths:
            cls, _ = au.deploy_class(path, self.config, host)
            with self.subTest(path):
                self.assertNotEqual(cls, "unclassified", f"{path}: add it to release_units.json deploy_classes")
            if cls in au.DEPLOY_CLASSES:
                deciding.add((cls, next(p for p in classes[cls] if au.glob_match(path, p))))
        for cls in au.DEPLOY_CLASSES:
            for pattern in classes[cls]:
                with self.subTest(cls=cls, pattern=pattern):
                    self.assertIn((cls, pattern), deciding, "no deploy/ path is decided by this pattern")

    def test_decided_classes(self):
        # docs/adr/20261027151755-unit-lane-deploy-path-classes.md, from the
        # paths that closed the lanes after rc.297.
        host = self.host()
        want = {
            "deploy/ansible/roles/control_plane_service/files/faas-apid.service": ("release_units", ["release_units"]),
            "deploy/ansible/roles/prometheus/files/faas.rules.yml": ("host_roles", ["host_config"]),
            "deploy/ansible/roles/grafana/files/frozen-tier.json": ("host_roles", ["host_config"]),
            "deploy/ansible/roles/seal_domains/tasks/main.yml": ("host_roles", ["compute_deploy", "host_config"]),
            "deploy/ansible/roles/vmmd_service/files/vmmd.toml.example": ("host_roles", ["compute_deploy"]),
            "deploy/ansible/group_vars/compute_nodes/vmmd_adoption.yml": ("host_roles", ["compute_deploy"]),
            "deploy/ansible/roles/gatewayd_internal_service/files/faas-realtimed.service": ("host_roles", ["compute_deploy"]),
            "deploy/ansible/node_join.yml": ("compute_only", ["compute_deploy"]),
            "deploy/ansible/roles/seal_domains/README.md": ("inert", []),
            "deploy/grafana/frozen-tier.json": ("inert", []),
            "deploy/manifest/README.md": ("inert", []),
            "deploy/systemd/faas-realtimed.service": ("inert", []),
            "deploy/managed-postgres.example.json": ("inert", []),
            "deploy/manifest/production/gcp-live.template.yaml": ("blocking", ["deploy"]),
        }
        for path, cls in want.items():
            with self.subTest(path):
                self.assertTrue((REPO / path).is_file())
                self.assertEqual(au.deploy_class(path, self.config, host), cls)

    def test_prometheus_and_grafana_converge_without_restarting_daemons(self):
        # Their rule and dashboard files ride the control-plane lane's
        # convergence; the roles reload their own service and notify no
        # other role's handlers, so no FaaS daemon or database restarts.
        tree = host_roles.fs_tree(str(REPO))
        order = host_roles.group_hashes(tree, "control_plane")["order"]
        restart = host_roles.restart_roles(tree, "control_plane", ("faas-", "postgresql", "pgbouncer"))
        deps = host_roles.notify_targets(tree, order)
        for role in ("prometheus", "grafana"):
            with self.subTest(role):
                self.assertIn(role, order)
                self.assertNotIn(role, restart)
                self.assertNotIn(role, deps)
        self.assertEqual(self.config["host_config"]["converged_groups"], ["control_plane"])

    def test_inert_paths_are_not_released(self):
        # No release workflow reads an inert path or tree; a role that names
        # one of their files makes that file host config instead.
        host = self.host()
        inert = [p for p in self.deploy_paths() if au.deploy_class(p, self.config, host)[0] == "inert"]
        trees = [p for p in self.config["deploy_classes"]["inert"] if p.endswith("/")]
        for workflow in ("release.yml", "release-units.yml", "cd-controlplane.yml", "cd-compute.yml",
                         "cd-platform.yml", "cd-edge-units.yml", "cd-edge-node.yml", "cd-unit-watch.yml"):
            path = REPO / ".github/workflows" / workflow
            if not path.exists():
                continue
            text = path.read_text(encoding="utf-8")
            for name in trees + inert:
                with self.subTest(workflow=workflow, path=name):
                    self.assertNotIn(name, text)
        # deploy/grafana/ is a byte-identical mirror of the role's copies.
        for dashboard in sorted((REPO / "deploy/grafana").glob("*.json")):
            with self.subTest(dashboard=dashboard.name):
                role_copy = REPO / "deploy/ansible/roles/grafana/files" / dashboard.name
                if role_copy.exists():
                    self.assertEqual(dashboard.read_bytes(), role_copy.read_bytes())

    def test_converged_roles_never_include_compute_only_roles(self):
        # A path only compute roles read waits for the full release; a path
        # control-plane roles read rides the lane. The split comes from the
        # plays themselves (host_roles.py), so a compute role the control
        # plane starts to run moves its paths to host_config.
        tree = host_roles.fs_tree(str(REPO))
        cp = host_roles.group_owners(tree, "control_plane")
        compute = host_roles.group_owners(tree, "compute_nodes")
        for path in ("deploy/ansible/roles/vmmd_service/tasks/main.yml",
                     "deploy/ansible/roles/gatewayd_internal_service/files/faas-gatewayd-internal.service"):
            self.assertIn(path, compute)
            self.assertNotIn(path, cp)
        self.assertIn("deploy/ansible/roles/postgres_capacity/defaults/main.yml", cp)
        # The legacy unit tree is outside releases on the control plane
        # except for the files postgres_backup installs from it.
        unread = sorted(p for p in cp if p.startswith("deploy/systemd/"))
        self.assertTrue(unread)
        self.assertTrue(all("postgres_backup" in cp[p] for p in unread), unread)

    def test_deployable_lanes_name_services(self):
        for name, entry in self.config["binaries"].items():
            if entry["lane"] in au.DEPLOY_LANES:
                self.assertTrue(entry.get("services"), name)

    def test_edge_lane_is_the_gateways(self):
        # The edge lane rolls host by host without draining: only the
        # gateways hand traffic over that way. vmmd and compute schedd are
        # the vmmd session's (restart survival, ADR-1736 and node drain).
        edge = sorted(n for n, e in self.config["binaries"].items() if e["lane"] == "edge")
        self.assertEqual(edge, ["gatewayd-internal", "gatewayd-public"])


@unittest.skipUnless(shutil.which(os.environ.get("GO", "go")), "go toolchain not available")
class EndToEndTest(unittest.TestCase):
    """report() on a throwaway module: real go list, real git history."""

    def run_git(self, *args):
        subprocess.run([*GIT, *args], cwd=self.root, check=True, capture_output=True)

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

    def data_config(self):
        with open(self.config, encoding="utf-8") as fh:
            config = json.load(fh)
        config["migrations_dir"] = "migrations/"
        with open(self.config, "w", encoding="utf-8") as fh:
            json.dump(config, fh)

    def test_report_applies_expand_migrations_and_blocks_on_contract(self):
        self.data_config()
        self.write("schema.sql", "CREATE TABLE public.apps (\n);\n")
        self.commit("schema")
        self.run_git("tag", "v0.1.0")
        self.write("pkg/api/api.go", "package api\n\nfunc Serve() { _ = 3 }\n")
        self.write("migrations/20261018000000001_note.sql",
                   "-- migration-compat: expand\n-- +goose Up\nSET LOCAL lock_timeout = '5s';\n"
                   "ALTER TABLE apps ADD COLUMN note text;\n")
        expand = self.commit("apid + expand migration")
        rep = au.report("v0.1.0", expand, self.config, root=self.root)
        self.assertTrue(rep["eligible"], rep["reasons"])
        self.assertEqual(rep["data"]["version"], f"data-{expand}")
        self.assertEqual([m["path"] for m in rep["data"]["migrations"]], ["migrations/20261018000000001_note.sql"])
        self.assertEqual(rep["deployable"], ["apid"])
        # A contract migration later in the range closes the lane.
        self.write("migrations/20261018000000002_drop.sql",
                   "-- migration-compat: contract\n-- +goose Up\nSET LOCAL lock_timeout = '5s';\n"
                   "ALTER TABLE apps DROP COLUMN note;\n")
        head = self.commit("contract migration")
        rep = au.report("v0.1.0", head, self.config, root=self.root)
        self.assertFalse(rep["eligible"])
        self.assertEqual(rep["blocked_by"], ["migrations"])
        self.assertIsNone(rep["data"]["version"])

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


def lib(a="1", b="2", above_b=""):
    """pkg/lib with two functions the linker keeps out of line, so their
    code and line-table rows stay their own."""
    return (f"package lib\n\n//go:noinline\nfunc A() int {{ return {a} }}\n\n"
            f"{above_b}//go:noinline\nfunc B() int {{ return {b} }}\n")


@unittest.skipUnless(shutil.which(os.environ.get("GO", "go")), "go toolchain not available")
class CodeEndToEndTest(unittest.TestCase):
    """report(code=True) on a throwaway module: real builds at base and head."""

    def run_git(self, *args):
        subprocess.run([*GIT, *args], cwd=self.root, check=True, capture_output=True)

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
        self.write("Makefile", "LDFLAGS := -X example.com/m/pkg/wire.Version=$(VERSION)\n"
                               "GO_BUILD_FLAGS := -trimpath -buildvcs=false\n"
                               "DAEMONS := apid meterd\nCLIS := gregalectl\n\n"
                               "build-release-unit:\n\tgo build $(GO_BUILD_FLAGS)\n")
        self.write("pkg/wire/wire.go", 'package wire\n\nvar Version = "dev"\n')
        # apid calls lib.A and meterd lib.B: both import pkg/lib, and the
        # linker keeps only the function each one reaches.
        self.write("cmd/apid/main.go",
                   'package main\n\nimport (\n\t"example.com/m/pkg/lib"\n\t"example.com/m/pkg/wire"\n)\n\n'
                   'func main() { println(lib.A(), wire.Version) }\n')
        self.write("cmd/meterd/main.go",
                   'package main\n\nimport "example.com/m/pkg/lib"\n\nfunc main() { println(lib.B()) }\n')
        self.write("cmd/gregalectl/main.go", "package main\n\nfunc main() {}\n")
        self.write("pkg/lib/lib.go", lib())
        self.config = os.path.join(self.root, "lanes.json")
        with open(self.config, "w", encoding="utf-8") as fh:
            json.dump({"binaries": {"apid": {"lane": "control-plane", "services": [{"name": "apid"}]},
                                    "meterd": {"lane": "control-plane", "services": [{"name": "meterd"}]},
                                    "gregalectl": {"lane": "tool"}},
                       "global_inputs": ["go.mod", "go.sum"],
                       "blocking_paths": {"migrations": ["migrations/"]}}, fh)
        self.base = self.commit("base")

    def report(self, **kw):
        return au.report(self.base, "HEAD", self.config, root=self.root, code=True, workdir=self.root, **kw)

    def test_code_narrows_what_restarts_but_the_lane_carries_the_closure(self):
        # Same line count, so apid's line table does not move either.
        self.write("pkg/lib/lib.go", lib(b="3"))
        head = self.commit("change B")
        rep = self.report()
        self.assertEqual(rep["code_selection"]["measured"], ["apid", "meterd"])
        apid, meterd = rep["binaries"]["apid"], rep["binaries"]["meterd"]
        self.assertEqual((apid["code"], apid["changed"]), ("same", False))
        self.assertEqual(apid["code_digest"], apid["base_code_digest"])
        self.assertEqual((meterd["code"], meterd["changed"]), ("changed", True))
        self.assertNotEqual(meterd["code_digest"], meterd["base_code_digest"])
        self.assertEqual((apid["version"], meterd["version"]), (f"unit-{head}", f"unit-{head}"))
        self.assertEqual(rep["lanes"]["control-plane"]["changed"], ["meterd"])
        self.assertEqual(rep["deployable"], ["apid", "meterd"])

    def test_a_line_shift_is_not_a_code_change(self):
        self.write("pkg/lib/lib.go", lib(above_b="// B moved down a line.\n"))
        self.commit("comment above B")
        rep = self.report()
        self.assertEqual(rep["binaries"]["meterd"]["code"], "lines")
        self.assertEqual(rep["binaries"]["apid"]["code"], "same")
        self.assertEqual(rep["lanes"]["control-plane"]["changed"], [])
        self.assertTrue(rep["eligible"])

    def test_base_digests_come_from_the_cache_on_later_runs(self):
        cache = os.path.join(self.root, "base-code.json")
        self.write("pkg/lib/lib.go", lib(a="4"))
        self.commit("change A")
        first = self.report(code_cache=cache)
        self.assertEqual(first["code_selection"]["base_cached"], [])
        self.assertEqual(first["code_selection"]["base_built"], ["apid", "meterd"])
        with open(cache, encoding="utf-8") as fh:
            self.assertEqual(sorted(json.load(fh)["digests"]), ["apid", "meterd"])
        second = self.report(code_cache=cache)
        self.assertEqual(second["code_selection"]["base_cached"], ["apid", "meterd"])
        self.assertEqual(second["code_selection"]["base_built"], [], "nothing new to cache")
        self.assertEqual(second["binaries"], first["binaries"])
        self.assertEqual(second["binaries"]["apid"]["code"], "changed")

    def test_a_failed_code_build_falls_back_to_the_closure(self):
        self.write("pkg/lib/lib.go", lib(a=""))
        self.base = self.commit("broken base")
        self.write("pkg/lib/lib.go", lib(a="8"))
        self.commit("fixed")
        rep = self.report()
        self.assertIn("go build at", rep["code_selection"]["error"])
        self.assertEqual(rep["code_selection"]["measured"], [])
        self.assertTrue(rep["binaries"]["apid"]["changed"] and rep["binaries"]["meterd"]["changed"])
        self.assertNotIn("code_digest", rep["binaries"]["apid"])

    def test_a_partial_clone_fetches_the_base_files_in_one_request(self):
        self.write("pkg/lib/lib.go", lib(b="9"))
        self.commit("change B")
        self.run_git("config", "uploadpack.allowFilter", "true")
        clone = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, clone)
        subprocess.run([*GIT, "clone", "-q", "--filter=blob:none", f"file://{self.root}", clone],
                       check=True, capture_output=True)

        def missing():
            out = subprocess.run([*GIT, "rev-list", "--objects", "--missing=print", self.base], cwd=clone,
                                 check=True, capture_output=True, text=True).stdout
            return [line for line in out.splitlines() if line.startswith("?")]

        self.assertEqual(len(missing()), 1, "base's pkg/lib/lib.go is not in the checkout")
        head = subprocess.run(["git", "rev-parse", "HEAD"], cwd=clone, check=True, capture_output=True,
                              text=True).stdout.strip()
        self.assertEqual(au.binary_code.prefetch_tree(self.base, head, clone), 1)
        self.assertEqual(missing(), [])
        self.assertEqual(au.binary_code.prefetch_tree(self.base, head, self.root), 0, "not a partial clone")

    def test_a_blocked_range_builds_nothing(self):
        self.write("pkg/lib/lib.go", lib(b="5"))
        self.write("migrations/00002_x.sql", "select 1;\n")
        self.commit("meterd + migration")
        rep = self.report()
        self.assertEqual(rep["code_selection"]["measured"], [])
        self.assertTrue(rep["binaries"]["apid"]["changed"], "unmeasured binaries keep the closure")
        self.assertFalse(rep["eligible"])


if __name__ == "__main__":
    unittest.main()
