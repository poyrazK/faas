#!/usr/bin/env python3
import datetime as dt
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("verdict", Path(__file__).with_name("queue-bindings-native-verdict.py"))
verdict = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verdict)


class VerdictTests(unittest.TestCase):
    def events(self):
        events = [dict(Action="start"), dict(Test=verdict.ROOT, Action="run")]
        for phase in verdict.PHASES:
            events += [dict(Test=verdict.ROOT+"/"+phase, Action=action) for action in ("run", "pass")]
        manifest = dict(version=1, source_revision="a"*40, template="queue-worker", runtime="node22",
                        artifacts={name: "b"*64 for name in verdict.CONTRACT["artifacts"]})
        events += [dict(Test=verdict.ROOT, Action="output", Output="native_test.go:1: "+verdict.IDENTITY_PREFIX+json.dumps(manifest)+"\n"),
                   dict(Test=verdict.ROOT, Action="pass"), dict(Action="pass")]
        return events

    def data(self, events):
        start = dt.datetime(2026, 10, 15, tzinfo=dt.UTC)
        return "\n".join(json.dumps(dict(event, Package=verdict.PACKAGE, Time=(start+dt.timedelta(seconds=index)).isoformat()))
                         for index, event in enumerate(events)).encode()

    def report(self, events):
        return verdict.make_report(self.data(events), "native", True, True, True)

    def test_complete_and_fragmented_cleanup_identity(self):
        for size in (256, 1024, 16384):
            events = self.events()
            event = events[-3]
            line = event["Output"]
            events[-3:-2] = [dict(event, Output=line[offset:offset+size]) for offset in range(0, len(line), size)]
            report = self.report(events)
            self.assertTrue(report["native_qualified"])
            self.assertEqual(len(report["checks"]), 9)

    def test_every_phase_and_parent_package_are_required(self):
        for name in [verdict.ROOT, *[verdict.ROOT+"/"+p for p in verdict.PHASES]]:
            with self.subTest(name=name), self.assertRaises(ValueError):
                self.report([event for event in self.events() if event.get("Test") != name])
        with self.assertRaises(ValueError):
            self.report(self.events()[:-1])

    def test_skip_failure_unknown_reorder_overlap_and_duplicate_cannot_qualify(self):
        for change in ("skip", "fail", "unknown", "reorder", "overlap", "duplicate"):
            with self.subTest(change=change), self.assertRaises(ValueError):
                events = self.events()
                if change in ("skip", "fail"):
                    events[3]["Action"] = change
                elif change == "unknown":
                    events[2]["Test"] += "_foreign"
                elif change == "reorder":
                    events[2:6] = events[4:6]+events[2:4]
                elif change == "overlap":
                    events[3], events[4] = events[4], events[3]
                else:
                    events.insert(4, dict(events[3]))
                self.report(events)

    def test_identity_must_follow_cleanup_and_precede_parent_pass(self):
        for change in ("early", "late", "duplicate", "truncated", "oversized", "interrupted"):
            with self.subTest(change=change), self.assertRaises(ValueError):
                events = self.events()
                identity = events[-3]
                if change == "early":
                    events.insert(2, events.pop(-3))
                elif change == "late":
                    events[-3], events[-2] = events[-2], events[-3]
                elif change == "duplicate":
                    events.insert(-2, dict(identity))
                elif change == "truncated":
                    identity["Output"] = identity["Output"][:120]
                elif change == "oversized":
                    identity["Output"] = verdict.IDENTITY_PREFIX + "x"*verdict.MAX_IDENTITY_BYTES
                else:
                    identity["Output"] = identity["Output"][:120]
                    events.insert(-2, dict(identity, Test=verdict.ROOT+"/"+verdict.PHASES[-1]))
                self.report(events)

    def test_missing_foreign_and_changed_runtime_artifacts_rejected(self):
        for change in ["version", "boolean", "runtime", "template", "revision", "secret", "foreign", *verdict.CONTRACT["artifacts"]]:
            with self.subTest(change=change), self.assertRaises(ValueError):
                events = self.events()
                identity = events[-3]
                manifest = json.loads(identity["Output"].split(verdict.IDENTITY_PREFIX)[1])
                if change in verdict.CONTRACT["artifacts"]:
                    del manifest["artifacts"][change]
                elif change == "version":
                    manifest["version"] = 0
                elif change == "boolean":
                    manifest["version"] = True
                elif change == "revision":
                    manifest["source_revision"] = "dirty"
                elif change == "foreign":
                    manifest["artifacts"]["deploy_base"] = "b"*64
                elif change == "secret":
                    manifest["private_token"] = "must-not-enter-evidence"
                else:
                    manifest[change] = "foreign"
                identity["Output"] = verdict.IDENTITY_PREFIX+json.dumps(manifest)+"\n"
                self.report(events)

    def test_host_cleanup_and_source_requirements(self):
        data = self.data(self.events())
        for host, clean, before, after in [("unknown", True, True, True), ("native", False, True, True),
                                           ("native", True, False, True), ("native", True, True, False)]:
            with self.assertRaises(ValueError):
                verdict.make_report(data, host, clean, before, after)
        report = verdict.make_report(data, "nested-diagnostic", True, True, True)
        self.assertFalse(report["native_qualified"])
        for ttl in (0, True, 86400.0, 7*86400+1):
            with self.assertRaises(ValueError):
                verdict.make_report(data, "native", True, True, True, ttl)

    def test_report_omits_output_is_private_and_cannot_overwrite(self):
        events = self.events()
        events.insert(2, dict(Test=verdict.ROOT, Action="output", Output="private-canary-business-output\n"))
        report = self.report(events)
        self.assertNotIn("private-canary-business-output", json.dumps(report))
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)/"report.json"
            verdict.write_report(path, report)
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            original = path.read_bytes()
            link = Path(directory)/"link.json"
            link.symlink_to(path)
            for target in (path, link):
                with self.assertRaises(FileExistsError):
                    verdict.write_report(target, report)
                self.assertEqual(path.read_bytes(), original)


if __name__ == "__main__":
    unittest.main()
