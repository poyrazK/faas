#!/usr/bin/env python3
"""Receiver observations must not invent receipts or successful idle traffic."""

import importlib.machinery
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "deploy/ansible/roles/notification_receiver_metrics/files/faas-notification-receiver-metrics"
LOADER = importlib.machinery.SourceFileLoader("notification_receiver_metrics", str(SCRIPT))
SPEC = importlib.util.spec_from_loader(LOADER.name, LOADER)
COLLECTOR = importlib.util.module_from_spec(SPEC)
LOADER.exec_module(COLLECTOR)


def exposition(requests=0, failures=0, generation=100, count=None, receiver="faas-page", integration="webhook"):
    labels = f'receiver_name="{receiver}",integration="{integration}"'
    return "\n".join([
        f"process_start_time_seconds {generation}",
        f"alertmanager_notification_requests_total{{{labels}}} {requests}",
        f"alertmanager_notification_requests_failed_total{{{labels}}} {failures}",
        f"alertmanager_notification_latency_seconds_count{{{labels}}} {requests if count is None else count}",
        f"alertmanager_notification_latency_seconds_sum{{{labels}}} {requests * 0.1}",
    ]) + "\n"


class ReceiverMetricsTest(unittest.TestCase):
    def test_first_snapshot_does_not_invent_historical_delivery(self):
        state = COLLECTOR.observe(COLLECTOR.parse(exposition(10)), {}, 1000)
        self.assertEqual(state["pairs"]["faas-page/webhook"]["last_success"], 0)
        idle = COLLECTOR.observe(COLLECTOR.parse(exposition(10)), state, 1030)
        self.assertEqual(idle["pairs"]["faas-page/webhook"]["last_success"], 0)

    def test_success_needs_two_observations_and_failures_never_advance_it(self):
        state = COLLECTOR.observe(COLLECTOR.parse(exposition()), {}, 1000)
        state = COLLECTOR.observe(COLLECTOR.parse(exposition(1)), state, 1030)
        self.assertEqual(state["pairs"]["faas-page/webhook"]["last_success"], 0)
        state = COLLECTOR.observe(COLLECTOR.parse(exposition(1)), state, 1060)
        self.assertEqual(state["pairs"]["faas-page/webhook"]["last_success"], 1060)
        for at in (1090, 1120):
            state = COLLECTOR.observe(COLLECTOR.parse(exposition(2, 1)), state, at)
            self.assertEqual(state["pairs"]["faas-page/webhook"]["last_success"], 1060)

    def test_continuous_traffic_can_confirm_success_without_waiting_for_idle(self):
        state = COLLECTOR.observe(COLLECTOR.parse(exposition()), {}, 1000)
        for i in range(1, 4):
            state = COLLECTOR.observe(COLLECTOR.parse(exposition(i)), state, 1000 + i * 30)
        self.assertEqual(state["pairs"]["faas-page/webhook"]["last_success"], 1090)

    def test_late_failure_cannot_turn_a_candidate_into_a_receipt(self):
        state = COLLECTOR.observe(COLLECTOR.parse(exposition()), {}, 1000)
        state = COLLECTOR.observe(COLLECTOR.parse(exposition(1)), state, 1030)
        state = COLLECTOR.observe(COLLECTOR.parse(exposition(1, 1)), state, 1060)
        self.assertEqual(state["pairs"]["faas-page/webhook"]["last_success"], 0)

    def test_restart_and_config_reload_rebaseline_counters(self):
        state = COLLECTOR.observe(COLLECTOR.parse(exposition()), {}, 1000)
        state = COLLECTOR.observe(COLLECTOR.parse(exposition(1)), state, 1030)
        state = COLLECTOR.observe(COLLECTOR.parse(exposition(1)), state, 1060)
        for generation in (100, 200):
            with self.subTest(generation=generation):
                reset = COLLECTOR.observe(COLLECTOR.parse(exposition(generation=generation)), state, 1090)
                self.assertEqual(reset["pairs"]["faas-page/webhook"]["last_success"], 1060)
                reset = COLLECTOR.observe(COLLECTOR.parse(exposition(generation=generation)), reset, 1120)
                self.assertEqual(reset["pairs"]["faas-page/webhook"]["last_success"], 1060)

    def test_invalid_or_unscoped_source_is_rejected(self):
        cases = [exposition(1, 2), exposition(1, count=2), exposition(0.5),
                 exposition(generation="NaN"), exposition(receiver="private-secret"),
                 exposition(integration="private-token"), exposition().replace('receiver_name="faas-page",', ''),
                 exposition() + exposition(), exposition().replace('requests_total{', 'requests_total{bad="x",')]
        for source in cases:
            with self.subTest(source_length=len(source)):
                with self.assertRaises(ValueError):
                    COLLECTOR.parse(source)

    def test_export_failure_removes_cached_receiver_values(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            state_path, textfile = root / "state.json", root / "metrics.prom"
            source = lambda: COLLECTOR.parse(exposition())
            self.assertEqual(COLLECTOR.run(state_path, textfile, now=lambda: 1000, source=source), 0)
            saved = state_path.read_text()

            def failed():
                raise OSError("private upstream address")

            self.assertEqual(COLLECTOR.run(state_path, textfile, now=lambda: 1030, source=failed), 1)
            body = textfile.read_text()
            self.assertIn("faas_notification_receiver_collector_up 0", body)
            self.assertNotIn('receiver="faas-page"', body)
            self.assertNotIn("private", body)
            self.assertEqual(saved, state_path.read_text())

    def test_missing_corrupt_and_future_state_do_not_create_receipts(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "state.json"
            for body in ('{}', '[]', '{"pairs":[]}', '{"pairs":{"x":{"pending":"bad"}}}', 'not json'):
                path.write_text(body)
                self.assertEqual(COLLECTOR.load_state(path), {})
            initial = COLLECTOR.observe(COLLECTOR.parse(exposition()), {}, 1000)
            initial["pairs"]["faas-page/webhook"]["last_success"] = 9999
            current = COLLECTOR.observe(COLLECTOR.parse(exposition()), initial, 1030)
            self.assertEqual(current["pairs"]["faas-page/webhook"]["last_success"], 0)


if __name__ == "__main__":
    unittest.main()
