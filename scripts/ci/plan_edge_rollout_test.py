#!/usr/bin/env python3
"""Unit tests for plan_edge_rollout.py (edge lane canary order, gate choice
and the 30-minute budget)."""
import importlib.util
import json
import pathlib
import sys
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).parent))
spec = importlib.util.spec_from_file_location(
    "plan_edge_rollout", pathlib.Path(__file__).with_name("plan_edge_rollout.py"))
per = importlib.util.module_from_spec(spec)
spec.loader.exec_module(per)

PUBLIC = {"lane": "edge", "services": [
    {"name": "gatewayd-public", "host": "control-plane"},
    {"name": "gatewayd-public-edge", "host": "compute-only", "optional": True}]}
INTERNAL = {"lane": "edge", "services": [{"name": "gatewayd-internal", "host": "compute-only"}]}
APID = {"lane": "control-plane", "services": [{"name": "apid"}]}
NODES = [{"node": f"fsn-{i}", "ssh_host": f"10.0.0.{i}"} for i in (2, 3, 4)]


def manifest(**units):
    return {"units": units}


class CanaryOrderTest(unittest.TestCase):
    def test_default_canary_is_the_first_target(self):
        got = per.plan(manifest(**{"gatewayd-public": PUBLIC, "gatewayd-internal": INTERNAL}), NODES)
        self.assertEqual(got["canary"]["node"], "fsn-2")
        self.assertEqual([t["node"] for t in got["followers"]], ["fsn-3", "fsn-4"])
        # The control plane's gateway follows the node canary.
        self.assertEqual(got["control_plane"], {"deploy": True, "gate": "follower"})

    def test_designated_canary_goes_first_and_the_rest_keep_their_order(self):
        got = per.plan(manifest(**{"gatewayd-internal": INTERNAL}), NODES, canary="fsn-3")
        self.assertEqual(got["canary"]["node"], "fsn-3")
        self.assertEqual([t["node"] for t in got["followers"]], ["fsn-2", "fsn-4"])
        self.assertEqual(got["control_plane"], {"deploy": False, "gate": "follower"})

    def test_control_plane_is_the_canary_when_no_node_restarts_a_gateway(self):
        # gatewayd-public alone: nodes only carry the optional edge unit, so
        # the control plane is the first host that runs the new code.
        got = per.plan(manifest(**{"gatewayd-public": PUBLIC}), NODES, canary="fsn-3")
        self.assertIsNone(got["canary"])
        self.assertEqual(got["control_plane"], {"deploy": True, "gate": "canary"})
        self.assertEqual([t["node"] for t in got["followers"]], ["fsn-2", "fsn-3", "fsn-4"])

    def test_no_edge_units_touches_no_host(self):
        got = per.plan(manifest(apid=APID), NODES)
        self.assertEqual(got, {"canary": None, "followers": [], "control_plane": {"deploy": False, "gate": "canary"}})

    def test_refusals(self):
        both = manifest(**{"gatewayd-public": PUBLIC, "gatewayd-internal": INTERNAL})
        for name, targets, canary, want in [
            ("unknown canary", NODES, "fsn-9", "not one of the edge targets"),
            ("node gateway without targets", [], "", "list the edge targets"),
            ("duplicate node", NODES + NODES[:1], "", "twice"),
        ]:
            with self.subTest(name):
                with self.assertRaises(per.PlanError) as ctx:
                    per.plan(both, targets, canary)
                self.assertIn(want, str(ctx.exception))


class GateConfigTest(unittest.TestCase):
    def test_repository_gates_are_valid_and_the_canary_is_longer(self):
        gates = per.load_gates()
        self.assertGreater(gates["canary"]["minutes"], gates["follower"]["minutes"])

    def test_bad_gate_config_is_refused(self):
        for gates in ({"canary": {"duration": "1m", "max_hold": "2m"}, "follower": {"duration": "1m", "max_hold": "10s"}},
                      {"canary": {"duration": "soon", "max_hold": "2m"}, "follower": {"duration": "1m", "max_hold": "10s"}}):
            with self.subTest(gates), tempfile.NamedTemporaryFile("w", suffix=".json") as fh:
                json.dump({"gates": gates}, fh)
                fh.flush()
                with self.assertRaises(per.PlanError):
                    per.load_gates(fh.name)

    def test_three_node_release_touching_both_lanes_fits_in_30_minutes(self):
        gates = per.load_gates()
        minutes = per.budget_minutes(gates, nodes=3)
        self.assertLessEqual(minutes, per.BUDGET_MINUTES,
                             f"both lanes on three nodes take {minutes:.1f} min with these gates")
        # Two full gates (control-plane lane, edge canary), three follower gates.
        expected = (per.BUILD_MINUTES + per.EDGE_PLAN_MINUTES + 5 * per.HOST_DEPLOY_MINUTES
                    + 2 * gates["canary"]["minutes"] + 3 * gates["follower"]["minutes"])
        self.assertAlmostEqual(minutes, expected)


if __name__ == "__main__":
    unittest.main()
