#!/usr/bin/env python3
"""Unit tests for plan_unit_deploy.py (per-unit deploy planning)."""
import copy
import importlib.util
import pathlib
import sys
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).parent))
spec = importlib.util.spec_from_file_location(
    "plan_unit_deploy", pathlib.Path(__file__).with_name("plan_unit_deploy.py"))
pud = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pud)

BASE = "b" * 40
T1, T2 = "1" * 40, "2" * 40
CP, NODE = "control-plane", "compute-only"
APID = {"lane": CP, "version": "unit-" + "a" * 40, "services": [
    {"name": "apid", "readyz": "http://127.0.0.1:9101/readyz"}, {"name": "apid-clone-worker", "optional": True}]}
METERD = {"lane": CP, "version": "unit-" + "c" * 40, "services": [
    {"name": "meterd", "readyz": "http://127.0.0.1:9106/readyz"}]}
S3 = {"lane": CP, "version": "unit-" + "d" * 40, "services": [
    {"name": "s3-gatewayd", "optional": True, "readyz": "http://127.0.0.1:9096/readyz"}]}
BRIDGED = {"lane": CP, "version": "unit-" + "9" * 40, "services": [{"name": "bridged", "optional": True}]}
PUBLIC = {"lane": "edge", "version": "unit-" + "e" * 40, "services": [
    {"name": "gatewayd-public", "host": CP, "readyz": "http://127.0.0.1:9092/readyz"},
    {"name": "gatewayd-public-edge", "host": NODE, "optional": True, "readyz": "http://127.0.0.1:9092/readyz"}]}
INTERNAL = {"lane": "edge", "version": "unit-" + "f" * 40, "services": [
    {"name": "gatewayd-internal", "host": NODE, "readyz": "http://127.0.0.1:9090/readyz"}]}


def release(commit=T2, **units):
    return {"format_version": 2, "kind": pud.KIND_RELEASE, "base_sha": BASE, "git_sha": commit,
            "units": units or {"apid": APID, "meterd": METERD}}


def full_host():
    return {"current": BASE, "base_git_sha": BASE, "unit_versions": None}


def overlay_host(units, commit=T1, lanes=None, replaced=("apid",)):
    lanes = lanes or {name: CP for name in units}
    return {"current": "e" * 40, "base_git_sha": BASE, "unit_versions": {
        "kind": pud.KIND_VERSIONS, "base_sha": BASE, "git_sha": commit, "units": units,
        "unit_lanes": lanes, "replaced": list(replaced)}}


def ancestor(a, d):
    return (a, d) in {(T1, T2), (T1, T1), (T2, T2)}


def cp_plan(rel, host, **kw):
    return pud.plan(rel, host, CP, CP, ancestor, **kw)


class ControlPlaneLaneTest(unittest.TestCase):
    def test_first_unit_release_over_a_full_release(self):
        got = cp_plan(release(), full_host())
        self.assertEqual(got["action"], "deploy")
        self.assertEqual(got["units"], ["apid", "meterd"])
        self.assertEqual(got["only"], ["apid", "meterd"])
        self.assertEqual(got["verify"], ["apid=http://127.0.0.1:9101/readyz", "?apid-clone-worker",
                                         "meterd=http://127.0.0.1:9106/readyz"])
        self.assertEqual(got["gate"], [["--unit", "apid"], ["--unit", "meterd"]])
        self.assertEqual(got["expect_current"], BASE)
        self.assertEqual(set(got["replace"]), {"bin/apid", "bin/meterd", "bin/deployctl", "unit-versions.json"})
        record = got["unit_versions"]
        self.assertEqual(record["units"], {"apid": APID["version"], "meterd": METERD["version"]})
        self.assertEqual(record["unit_lanes"], {"apid": CP, "meterd": CP})
        self.assertEqual(record["replaced"], ["apid", "meterd"])
        self.assertEqual(record["built_over"], BASE)

    def test_only_units_whose_version_moved_restart(self):
        got = cp_plan(release(), overlay_host({"apid": APID["version"]}))
        self.assertEqual(got["units"], ["meterd"])
        self.assertEqual(got["only"], ["meterd"])
        self.assertEqual(got["gate"], [["--unit", "meterd"]])
        self.assertNotIn("bin/apid", got["replace"])
        self.assertEqual(got["unit_versions"]["units"]["apid"], APID["version"])

    def test_optional_only_change_restarts_no_core_service(self):
        got = cp_plan(release(**{"s3-gatewayd": S3}), full_host())
        self.assertEqual(got["only"], [])
        self.assertEqual(got["verify"], ["?s3-gatewayd=http://127.0.0.1:9096/readyz"])

    def test_gate_judges_a_unit_it_does_not_know_by_component(self):
        got = cp_plan(release(bridged=BRIDGED), full_host())
        self.assertEqual(got["gate"], [["--unit", "bridged", "--component", "bridged"]])

    def test_release_id_is_deterministic_and_depends_on_inputs(self):
        a = cp_plan(release(), full_host())["release_id"]
        self.assertEqual(a, cp_plan(release(), full_host())["release_id"])
        self.assertRegex(a, r"^[0-9a-f]{40}$")
        moved = copy.deepcopy(release())
        moved["units"]["meterd"]["version"] = "unit-" + "f" * 40
        self.assertNotEqual(a, cp_plan(moved, full_host())["release_id"])

    def test_converge_reruns_the_active_overlays_units(self):
        host = overlay_host({"apid": APID["version"], "meterd": METERD["version"]}, replaced=("apid", "meterd"))
        got = cp_plan(release(), host)
        self.assertEqual(got["action"], "converge")
        self.assertEqual(got["release_id"], host["current"])
        self.assertEqual(got["only"], ["apid", "meterd"])
        self.assertEqual(got["gate"], [["--unit", "apid"], ["--unit", "meterd"]])
        self.assertEqual(got["replace"], {})

    def test_nothing_to_do_on_the_full_release(self):
        got = pud.plan(release(), full_host(), "edge", CP, ancestor)
        self.assertEqual(got["action"], "none")

    def test_refusals(self):
        cases = [
            ("N±1: base is not the previous release", release(), full_host(), {"previous_release": "8" * 40},
             "N±1"),
            ("another full release", release(), full_host() | {"base_git_sha": "9" * 40}, {}, "host runs full release"),
            ("newer unit release on host", release(commit=T1), overlay_host({"apid": APID["version"]}, commit=T2),
             {}, "not an ancestor"),
            ("host carries a unit this release lacks", release(apid=APID),
             overlay_host({"meterd": METERD["version"]}), {}, "does not carry: meterd"),
            ("versions for another base", release(),
             overlay_host({}) | {"unit_versions": overlay_host({})["unit_versions"] | {"base_sha": "9" * 40}},
             {}, "another base"),
            ("unexpected restart set", release(), full_host(), {"expect": ["apid"]}, "not the expected apid"),
            ("wrong format", release() | {"format_version": 1}, full_host(), {}, "not a format 2"),
        ]
        for name, rel, host, kw, want in cases:
            with self.subTest(name):
                with self.assertRaises(pud.Refused) as ctx:
                    cp_plan(rel, host, **kw)
                self.assertIn(want, str(ctx.exception))

    def test_previous_release_matching_the_base_passes(self):
        self.assertEqual(cp_plan(release(), full_host(), previous_release=BASE)["action"], "deploy")


class EdgeLaneTest(unittest.TestCase):
    REL = release(apid=APID, **{"gatewayd-public": PUBLIC, "gatewayd-internal": INTERNAL})

    def test_control_plane_lane_ignores_edge_units(self):
        got = cp_plan(self.REL, full_host())
        self.assertEqual(got["units"], ["apid"])
        self.assertEqual(got["unit_versions"]["units"], {"apid": APID["version"]})

    def test_edge_on_the_control_plane_follows_its_lane(self):
        # The CP lane already ran: the control plane carries apid's overlay.
        host = overlay_host({"apid": APID["version"]}, commit=T2)
        got = pud.plan(self.REL, host, "edge", CP, ancestor)
        self.assertEqual(got["units"], ["gatewayd-public"])
        self.assertEqual(got["only"], ["gatewayd-public"])
        self.assertEqual(got["verify"], ["gatewayd-public=http://127.0.0.1:9092/readyz"])
        self.assertEqual(got["gate"], [["--unit", "gatewayd-public"]])
        record = got["unit_versions"]
        self.assertEqual(record["units"], {"apid": APID["version"], "gatewayd-public": PUBLIC["version"]})
        self.assertEqual(record["unit_lanes"], {"apid": CP, "gatewayd-public": "edge"})
        self.assertEqual(record["replaced"], ["gatewayd-public"])

    def test_edge_on_a_compute_node(self):
        got = pud.plan(self.REL, full_host(), "edge", NODE, ancestor)
        self.assertEqual(got["units"], ["gatewayd-internal", "gatewayd-public"])
        self.assertEqual(got["only"], ["gatewayd-internal"])
        self.assertEqual(got["verify"], ["gatewayd-internal=http://127.0.0.1:9090/readyz",
                                         "?gatewayd-public-edge=http://127.0.0.1:9092/readyz"])
        self.assertEqual(got["gate"], [["--unit", "gatewayd-internal"], ["--unit", "gatewayd-public"]])
        self.assertEqual(set(got["replace"]), {"bin/gatewayd-internal", "bin/gatewayd-public", "bin/deployctl",
                                               "unit-versions.json"})

    def test_a_blocked_lane_does_not_stop_the_other(self):
        # The host's edge overlay predates a release whose edge lane is
        # blocked (it carries no edge units); the CP lane still deploys.
        host = overlay_host({"gatewayd-public": PUBLIC["version"]}, lanes={"gatewayd-public": "edge"},
                            replaced=("gatewayd-public",))
        got = cp_plan(release(apid=APID), host)
        self.assertEqual(got["units"], ["apid"])
        self.assertEqual(got["unit_versions"]["units"]["gatewayd-public"], PUBLIC["version"])
        with self.assertRaises(pud.Refused):
            pud.plan(release(apid=APID), host, "edge", CP, ancestor)


DATA_V1, DATA_V2 = "data-" + "3" * 40, "data-" + "4" * 40
MIGRATION = {"path": "migrations/20261018000000001_a.sql", "version": 20261018000000001, "eligible": True}


def with_data(rel, version=DATA_V1, **data):
    rel = copy.deepcopy(rel)
    rel["data"] = {"eligible": True, "version": version, "migrations": [MIGRATION], "blockers": []} | data
    rel["tools"] = {"deployctl": {"sha256": "0" * 64}, "migrate": {"sha256": "1" * 64}}
    return rel


def applied(host, version=DATA_V1):
    host = copy.deepcopy(host)
    host["unit_versions"]["data_version"] = version
    return host


class DataStepTest(unittest.TestCase):
    """The control-plane lane applies lane-eligible migrations before it
    restarts anything (docs/adr/20261018084150-unit-lane-expand-migrations.md)."""

    def test_first_deploy_applies_the_data_step(self):
        got = cp_plan(with_data(release()), full_host())
        self.assertEqual(got["action"], "deploy")
        self.assertEqual(got["replace"]["bin/migrate"], "bin/migrate")
        self.assertEqual(got["migrations"], [MIGRATION["path"]])
        self.assertEqual(got["gate"], [["--unit", "apid"], ["--unit", "meterd"], ["--unit", "migrations"]])
        self.assertEqual(got["unit_versions"]["data_version"], DATA_V1)
        self.assertNotEqual(got["release_id"], cp_plan(release(), full_host())["release_id"])

    def test_cases(self):
        units = {"apid": APID["version"], "meterd": METERD["version"]}
        cases = [
            # name, release, host, action, applies migrations, recorded data version
            ("no migrations in the range", release(), full_host(), "deploy", False, None),
            ("applied already: units only", with_data(release()), applied(overlay_host({"apid": APID["version"]})),
             "deploy", False, DATA_V1),
            ("applied already, nothing moved: converge", with_data(release()),
             applied(overlay_host(units, replaced=("apid", "meterd"))), "converge", False, None),
            ("newer migrations over an overlay", with_data(release(), DATA_V2),
             applied(overlay_host({"apid": APID["version"]})), "deploy", True, DATA_V2),
            ("migrations alone still deploy", with_data(release(), DATA_V2),
             applied(overlay_host(units, replaced=("apid", "meterd"))), "deploy", True, DATA_V2),
            ("a rolled-back host applies again (a no-op for goose)", with_data(release()),
             overlay_host({"apid": APID["version"]}), "deploy", True, DATA_V1),
        ]
        for name, rel, host, action, applies, recorded in cases:
            with self.subTest(name):
                got = cp_plan(rel, host)
                self.assertEqual(got["action"], action)
                self.assertEqual("bin/migrate" in got["replace"], applies)
                self.assertEqual(["--unit", "migrations"] in got["gate"], applies)
                if action == "deploy":
                    self.assertEqual(got["unit_versions"].get("data_version"), recorded)

    def test_migrations_alone_restart_nothing(self):
        units = {"apid": APID["version"], "meterd": METERD["version"]}
        got = cp_plan(with_data(release(), DATA_V2), applied(overlay_host(units, replaced=("apid", "meterd"))))
        self.assertEqual((got["units"], got["only"], got["verify"]), ([], [], []))
        self.assertEqual(got["gate"], [["--unit", "migrations"]])
        self.assertEqual(set(got["replace"]), {"bin/migrate", "bin/deployctl", "unit-versions.json"})

    def test_edge_lane_waits_for_the_data_step(self):
        rel = with_data(release(apid=APID, **{"gatewayd-public": PUBLIC, "gatewayd-internal": INTERNAL}))
        before = overlay_host({"apid": APID["version"]}, commit=T2)
        with self.assertRaisesRegex(pud.Refused, "deploy order"):
            pud.plan(rel, before, "edge", CP, ancestor)
        got = pud.plan(rel, applied(before), "edge", CP, ancestor)
        self.assertEqual(got["units"], ["gatewayd-public"])
        self.assertNotIn("bin/migrate", got["replace"])
        self.assertEqual(got["unit_versions"]["data_version"], DATA_V1)
        # Compute hosts have no database: cd-edge-units checks the control
        # plane before any of them.
        self.assertEqual(pud.plan(rel, full_host(), "edge", NODE, ancestor)["action"], "deploy")

    def test_refusals(self):
        no_tool = with_data(release())
        del no_tool["tools"]["migrate"]
        cases = [
            ("ineligible verdict", with_data(release(), eligible=False), "not lane-eligible"),
            ("ineligible migration", with_data(release(), migrations=[MIGRATION | {"eligible": False}]), "not lane-eligible"),
            ("no version", with_data(release(), version=None), "not lane-eligible"),
            ("no migrate tool", no_tool, "no tools/migrate"),
        ]
        for name, rel, want in cases:
            with self.subTest(name):
                with self.assertRaisesRegex(pud.Refused, want):
                    cp_plan(rel, full_host())

    def test_release_ids_without_data_are_unchanged(self):
        # Pinned: overlay ids of releases without a data step keep the
        # slice 1 formula, so a rerun of an older release reuses its overlay.
        self.assertEqual(pud.overlay_id(BASE, T2, {"apid": "unit-a"}),
                         pud.hashlib.sha1(f"{pud.KIND_RELEASE}/1\n{BASE}\n{T2}\napid=unit-a\n".encode()).hexdigest())


if __name__ == "__main__":
    unittest.main()
