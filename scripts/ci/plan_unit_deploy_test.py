#!/usr/bin/env python3
"""Unit tests for plan_unit_deploy.py (per-unit deploy planning)."""
import copy
import importlib.util
import pathlib
import unittest

spec = importlib.util.spec_from_file_location(
    "plan_unit_deploy", pathlib.Path(__file__).with_name("plan_unit_deploy.py"))
pud = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pud)

BASE = "b" * 40
T1, T2 = "1" * 40, "2" * 40
APID = {"version": "unit-" + "a" * 40, "services": [
    {"name": "apid", "readyz": "http://127.0.0.1:9101/readyz"}, {"name": "apid-clone-worker", "optional": True}]}
METERD = {"version": "unit-" + "c" * 40, "services": [{"name": "meterd", "readyz": "http://127.0.0.1:9106/readyz"}]}
S3 = {"version": "unit-" + "d" * 40, "services": [
    {"name": "s3-gatewayd", "optional": True, "readyz": "http://127.0.0.1:9096/readyz"}]}


def release(commit=T2, **units):
    return {"format_version": 1, "kind": pud.KIND_RELEASE, "base_sha": BASE, "git_sha": commit,
            "units": units or {"apid": APID, "meterd": METERD}}


def full_host():
    return {"current": BASE, "base_git_sha": BASE, "unit_versions": None}


def overlay_host(units, commit=T1, services=("apid",)):
    return {"current": "e" * 40, "base_git_sha": BASE, "unit_versions": {
        "kind": pud.KIND_VERSIONS, "base_sha": BASE, "git_sha": commit, "units": units,
        "services": list(services), "verify": ["apid"]}}


def ancestor(a, d):
    return (a, d) in {(T1, T2), (T1, T1), (T2, T2)}


class PlanTest(unittest.TestCase):
    def test_first_unit_release_over_a_full_release(self):
        got = pud.plan(release(), full_host(), ancestor)
        self.assertEqual(got["action"], "deploy")
        self.assertEqual(got["changed"], ["apid", "meterd"])
        self.assertEqual(got["only"], ["apid", "meterd"])
        self.assertEqual(got["verify"], ["apid=http://127.0.0.1:9101/readyz", "?apid-clone-worker",
                                         "meterd=http://127.0.0.1:9106/readyz"])
        self.assertEqual(got["expect_current"], BASE)
        self.assertEqual(set(got["replace"]), {"bin/apid", "bin/meterd", "bin/deployctl", "unit-versions.json"})
        record = got["unit_versions"]
        self.assertEqual(record["units"], {"apid": APID["version"], "meterd": METERD["version"]})
        self.assertEqual(record["built_over"], BASE)
        self.assertEqual(record["services"], ["apid", "meterd"])

    def test_only_units_whose_version_moved_restart(self):
        host = overlay_host({"apid": APID["version"]})
        got = pud.plan(release(), host, ancestor)
        self.assertEqual(got["changed"], ["meterd"])
        self.assertEqual(got["only"], ["meterd"])
        self.assertNotIn("bin/apid", got["replace"])
        # The recorded versions stay complete: apid's carries over.
        self.assertEqual(got["unit_versions"]["units"]["apid"], APID["version"])

    def test_optional_only_change_restarts_no_core_service(self):
        got = pud.plan(release(**{"s3-gatewayd": S3}), full_host(), ancestor)
        self.assertEqual(got["only"], [])
        self.assertEqual(got["verify"], ["?s3-gatewayd=http://127.0.0.1:9096/readyz"])

    def test_release_id_is_deterministic_and_depends_on_inputs(self):
        a = pud.plan(release(), full_host(), ancestor)["release_id"]
        self.assertEqual(a, pud.plan(release(), full_host(), ancestor)["release_id"])
        self.assertRegex(a, r"^[0-9a-f]{40}$")
        moved = copy.deepcopy(release())
        moved["units"]["meterd"]["version"] = "unit-" + "f" * 40
        self.assertNotEqual(a, pud.plan(moved, full_host(), ancestor)["release_id"])

    def test_nothing_changed(self):
        self.assertEqual(pud.plan(release(apid=APID), full_host(), ancestor)["action"], "deploy")
        host = overlay_host({"apid": APID["version"], "meterd": METERD["version"]}, services=("apid", "meterd"))
        got = pud.plan(release(), host, ancestor)
        self.assertEqual(got["action"], "converge")
        self.assertEqual(got["release_id"], host["current"])
        self.assertEqual(got["only"], ["apid", "meterd"])
        self.assertEqual(got["replace"], {})

    def test_refusals(self):
        cases = [
            ("another full release", release(), full_host() | {"base_git_sha": "9" * 40}, None, "host runs full release"),
            ("newer unit release on host", release(commit=T1), overlay_host({"apid": APID["version"]}, commit=T2),
             None, "not an ancestor"),
            ("host carries a unit this release lacks", release(apid=APID),
             overlay_host({"meterd": METERD["version"]}), None, "does not carry: meterd"),
            ("versions for another base", release(),
             overlay_host({}) | {"unit_versions": overlay_host({})["unit_versions"] | {"base_sha": "9" * 40}},
             None, "another base"),
            ("unexpected restart set", release(), full_host(), ["apid"], "not the expected apid"),
            ("wrong kind", release() | {"kind": "other"}, full_host(), None, "not a format 1"),
        ]
        for name, rel, host, expect, want in cases:
            with self.subTest(name):
                with self.assertRaises(pud.Refused) as ctx:
                    pud.plan(rel, host, ancestor, expect)
                self.assertIn(want, str(ctx.exception))

    def test_expected_units_match(self):
        got = pud.plan(release(), full_host(), ancestor, expect=["meterd", "apid"])
        self.assertEqual(got["changed"], ["apid", "meterd"])


if __name__ == "__main__":
    unittest.main()
