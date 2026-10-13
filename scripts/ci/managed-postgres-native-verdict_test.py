#!/usr/bin/env python3
import datetime as dt
import hashlib
import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("verdict", Path(__file__).with_name("managed-postgres-native-verdict.py"))
verdict = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verdict)


class VerdictTests(unittest.TestCase):
    def events(self):
        events = []
        start = dt.datetime(2026, 10, 12, tzinfo=dt.timezone.utc)
        def emit(name, action, output=None):
            event = dict(Package=verdict.PACKAGE,
                         Time=(start + dt.timedelta(seconds=len(events))).isoformat(),
                         Action=action)
            if name:
                event["Test"] = name
            if output is not None:
                event["Output"] = output
            events.append(json.dumps(event))
        emit("", "start")
        emit(verdict.ROOT, "run")
        for phase in verdict.PHASES:
            emit(verdict.ROOT + "/" + phase, "run")
            emit(verdict.ROOT + "/" + phase, "pass")
        identity = dict(postgres_major=17, runtime=dict(version=1, source_revision="b"*40,
                        artifacts={name: "c"*64 for name in verdict.CONTRACT["artifacts"]}))
        emit(verdict.ROOT, "output", "native_test.go:1: " + verdict.IDENTITY_PREFIX + json.dumps(identity))
        emit(verdict.ROOT, "pass")
        emit("", "pass")
        return events

    def test_complete(self):
        verdict.verify(self.events())

    def test_parent_pass_cannot_hide_skipped_or_failed_phase(self):
        for action in ("skip", "fail"):
            with self.subTest(action=action), self.assertRaises(ValueError):
                events = self.events()
                bad = json.loads(events[2])
                bad.update(Test=verdict.ROOT + "/unexpected", Action=action)
                verdict.verify(events[:2] + [json.dumps(bad)] + events[2:])

    def test_omitted_phase(self):
        with self.assertRaises(ValueError):
            verdict.verify([line for line in self.events()
                            if json.loads(line).get("Test") != verdict.ROOT + "/" + verdict.PHASES[-1]])

    def test_old_evidence_without_pooled_recovery_is_rejected(self):
        old = [line for line in self.events()
               if json.loads(line).get("Test") != verdict.ROOT + "/pooled_disconnect_recovery"]
        with self.assertRaisesRegex(ValueError, "pooled_disconnect_recovery"):
            verdict.verify(old)

    def test_old_probe_only_evidence_is_rejected(self):
        old = [line for line in self.events()
               if "/starter_" not in json.loads(line).get("Test", "")]
        with self.assertRaisesRegex(ValueError, "starter_"):
            verdict.verify(old)

    def test_package_failure_after_cleanup(self):
        with self.assertRaises(ValueError):
            verdict.verify(self.events() + [json.dumps({"Action": "fail"})])

    def test_empty_and_invalid_evidence(self):
        for lines in ([], ["garbage"]):
            with self.subTest(lines=lines), self.assertRaises(ValueError):
                verdict.verify(lines)

    def test_report_contains_only_bounded_evidence(self):
        data = ("\n".join(self.events()) + "\n").encode()
        report = verdict.make_report(data, "native", True, True, True, 86400)
        self.assertEqual(report["results_sha256"], hashlib.sha256(data).hexdigest())
        self.assertEqual(len(report["checks"]), 13)
        self.assertEqual(set(report["runtime"]["artifacts"]), set(verdict.CONTRACT["artifacts"]))
        self.assertNotIn("Output", report)
        self.assertEqual(report["host_class"], "native")

    def test_report_requires_both_leakchecks_and_identity(self):
        data = ("\n".join(self.events()) + "\n").encode()
        for before, after in ((False, True), (True, False)):
            with self.subTest(before=before), self.assertRaises(ValueError):
                verdict.make_report(data, "native", True, before, after, 86400)
        missing = [line for line in self.events() if "Output" not in json.loads(line)]
        with self.assertRaises(ValueError):
            verdict.make_report("\n".join(missing).encode(), "native", True, True, True, 86400)

    def test_pass_without_run_and_duplicate_pass_are_rejected(self):
        events = self.events()
        name = verdict.ROOT + "/" + verdict.PHASES[0]
        with self.assertRaises(ValueError):
            verdict.verify([line for line in events
                            if not (json.loads(line).get("Test") == name and json.loads(line)["Action"] == "run")])
        with self.assertRaises(ValueError):
            verdict.verify(events[:4] + [events[3]] + events[4:])

    def test_package_pass_is_mandatory(self):
        with self.assertRaises(ValueError):
            verdict.verify(self.events()[:-1])

    def test_duplicate_identity_and_future_ttl_are_rejected(self):
        events = self.events()
        with self.assertRaises(ValueError):
            verdict.verify(events[:-2] + [events[-3]] + events[-2:])
        with self.assertRaises(ValueError):
            verdict.make_report("\n".join(events).encode(), "native", True, True, True, 7*86400+1)


if __name__ == "__main__":
    unittest.main()
