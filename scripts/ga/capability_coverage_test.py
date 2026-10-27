#!/usr/bin/env python3
"""Unit tests for capability_coverage.py, the ci-light gate that maps every
customer entry point to a row of docs/ga-capability-inventory.md."""
import contextlib
import importlib.util
import io
import pathlib
import unittest
from unittest import mock

HERE = pathlib.Path(__file__).parent
spec = importlib.util.spec_from_file_location("capability_coverage", HERE / "capability_coverage.py")
cc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cc)


def run(src):
    out = io.StringIO()
    with mock.patch.object(cc, "collect", return_value=src), mock.patch.object(cc.sys, "argv", ["capability_coverage.py"]):
        with contextlib.redirect_stdout(out):
            code = cc.main()
    return code, out.getvalue()


class CoverageGateTest(unittest.TestCase):
    def test_mapped_identifiers_leave_nothing_unmatched(self):
        _, out = run({"routes": {"/v1/apps/{slug}/crons"}, "daemons": {"schedd"}})
        self.assertIn("TOTAL UNMATCHED: 0", out)
        self.assertNotIn("EMPTY", out)

    def test_an_inventory_row_without_an_entry_point_fails(self):
        code, out = run({"routes": {"/v1/apps/{slug}/crons"}})
        self.assertEqual(code, 1)
        self.assertRegex(out, r"rows with no entry point mapped: [1-9]")

    def test_an_unmatched_identifier_fails(self):
        code, out = run({"routes": {"/v1/apps/{slug}/crons"}, "daemons": {"zzqx-unheard-of-daemon"}})
        self.assertEqual(code, 1)
        self.assertIn("UNMATCHED daemons: zzqx-unheard-of-daemon", out)

    def test_an_empty_source_fails(self):
        code, out = run({"routes": {"/v1/apps/{slug}/crons"}, "sdk_go": set()})
        self.assertEqual(code, 1)
        self.assertIn("EMPTY sdk_go", out)

    def test_a_rule_target_must_name_one_inventory_row(self):
        with mock.patch.object(cc, "RULES", [(r"^x$", "no inventory row has this text")]):
            with self.assertRaises(SystemExit) as raised:
                run({"routes": {"/x"}})
        self.assertIn("matches 0 rows", str(raised.exception))

    def test_route_keys_drop_path_parameters(self):
        self.assertEqual(cc.normalize("routes", "/v1/apps/{slug}/Crons/{id}"), "/v1/apps/crons/")
        self.assertEqual(cc.route_key("/v1/apps/crons/"), "apps/crons crons")


if __name__ == "__main__":
    unittest.main()
