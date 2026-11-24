#!/usr/bin/env python3
"""Unit tests for rollback_drill.py."""
import importlib.util
import pathlib
import unittest

HERE = pathlib.Path(__file__).parent
spec = importlib.util.spec_from_file_location("rollback_drill", HERE / "rollback_drill.py")
rd = importlib.util.module_from_spec(spec)
spec.loader.exec_module(rd)

READY = {"current": "r2", "history_last": "r2", "predecessor": "r1", "predecessor_installed": True,
         "watch_known_good": None, "watch_known_good_installed": None, "host_config_record": True, "retained": 3}
PREFLIGHT = {"lock": "free", "prometheus": {"ok": True, "alerts": []}}


def failed(rows):
    return [n for n, ok, _ in rows if not ok]


class JudgeTest(unittest.TestCase):
    def test_ready(self):
        self.assertEqual(failed(rd.judge(READY, PREFLIGHT, True)), [])

    def test_each_missing_fact_fails_its_check(self):
        cases = [
            ({"current": ""}, {}, True, ["active", "history"]),
            ({"history_last": "r1"}, {}, True, ["history"]),
            ({"predecessor_installed": False}, {}, True, ["predecessor"]),
            ({"predecessor": ""}, {}, True, ["predecessor"]),
            ({"watch_known_good": "r0", "watch_known_good_installed": False}, {}, True, ["known-good"]),
            ({"host_config_record": False}, {}, True, ["host-config"]),
            ({}, {"prometheus": {"ok": False, "error": "refused"}}, True, ["prometheus"]),
            ({}, {}, False, ["freeze"]),
        ]
        for ready, pre, label, want in cases:
            with self.subTest(want=want):
                rows = rd.judge(dict(READY, **ready), dict(PREFLIGHT, **pre), label)
                self.assertEqual(failed(rows), want)

    def test_an_installed_known_good_passes(self):
        rows = rd.judge(dict(READY, watch_known_good="r1", watch_known_good_installed=True), PREFLIGHT, True)
        self.assertEqual(failed(rows), [])

    def test_markdown(self):
        text = rd.markdown(rd.judge(dict(READY, predecessor_installed=False), PREFLIGHT, True))
        self.assertIn("| predecessor | FAIL | r1: missing |", text)


if __name__ == "__main__":
    unittest.main()
