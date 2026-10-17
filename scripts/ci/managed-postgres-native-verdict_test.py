#!/usr/bin/env python3
import datetime as dt
import hashlib
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("verdict", Path(__file__).with_name("managed-postgres-native-verdict.py"))
verdict = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verdict)


class VerdictTests(unittest.TestCase):
    def events(self):
        events = []
        start = dt.datetime(2026, 10, 12, tzinfo=dt.UTC)
        def emit(name, action, output=None):
            event = dict(Package=verdict.PACKAGE,
                         Time=(start + dt.timedelta(seconds=len(events))).isoformat(),
                         Action=action)
            if name:
                event["Test"] = name
            if output is not None:
                event["Output"] = output + "\n"
            events.append(json.dumps(event))
        emit("", "start")
        emit(verdict.ROOT, "run")
        for phase in verdict.PHASES:
            emit(verdict.ROOT + "/" + phase, "run")
            emit(verdict.ROOT + "/" + phase, "pass")
        identity = dict(postgres_major=17, runtime=dict(version=verdict.CONTRACT["version"], source_revision="b"*40,
                        artifacts={name: "c"*64 for name in verdict.CONTRACT["artifacts"]}))
        identity["runtime"]["artifacts"]["probe_app_layer"] = "d" * 64
        identity["runtime"]["artifacts"]["probe_source_archive"] = "e" * 64
        identity["runtime"]["artifacts"]["python312_app_layer"] = "0" * 64
        identity["runtime"]["artifacts"]["python312_source_archive"] = "1" * 64
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
        self.assertEqual(len(report["checks"]), 19)
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


    def raw_events(self):
        return [json.loads(line) for line in self.events()]

    def verify_events(self, events):
        start = dt.datetime(2026, 10, 12, tzinfo=dt.UTC)
        return verdict.verify(json.dumps(dict(event, Time=(start + dt.timedelta(seconds=index)).isoformat()))
                              for index, event in enumerate(events))

    def identity_event(self, events):
        return next(event for event in events if verdict.IDENTITY_PREFIX in event.get("Output", ""))

    def test_go_style_fragmented_cleanup_identity_is_accepted(self):
        for size in (256, 1024):
            with self.subTest(size=size):
                events = self.raw_events()
                event = self.identity_event(events)
                index = events.index(event)
                output = event["Output"]
                self.assertGreater(len(output), size)
                events[index:index + 1] = [dict(event, Output=output[offset:offset + size])
                                         for offset in range(0, len(output), size)]
                _started, _completed, identity = self.verify_events(events)
                self.assertEqual(identity["runtime"]["version"], 3)

    def test_truncated_interrupted_oversized_and_duplicate_fragment_identity_is_rejected(self):
        for change in ("truncated", "interrupted", "oversized", "duplicate"):
            with self.subTest(change=change), self.assertRaises(ValueError):
                events = self.raw_events()
                event = self.identity_event(events)
                index = events.index(event)
                output = event["Output"]
                event["Output"] = output[:120]
                if change == "interrupted":
                    events.insert(index + 1, dict(event, Test=verdict.ROOT + "/" + verdict.PHASES[-1], Output=output[120:]))
                elif change == "oversized":
                    events.insert(index + 1, dict(event, Output="x" * verdict.MAX_IDENTITY_BYTES))
                elif change == "duplicate":
                    events.insert(index + 1, dict(event, Output=output))
                self.verify_events(events)

    def test_phases_must_run_sequentially_in_contract_order(self):
        events = self.raw_events()
        for candidate in (events[:2] + events[4:6] + events[2:4] + events[6:],
                          events[:3] + [events[4]] + events[3:4] + events[5:]):
            with self.assertRaises(ValueError):
                self.verify_events(candidate)

    def test_cleanup_identity_must_follow_all_phases_and_precede_parent_pass(self):
        events = self.raw_events()
        identity = self.identity_event(events)
        index = events.index(identity)
        for candidate in (events[:index] + events[index + 1:],
                          events[:2] + [identity] + events[2:index] + events[index + 1:],
                          events[:index] + events[index + 1:index + 2] + [identity] + events[index + 2:],
                          events[:index] + [identity, identity] + events[index + 1:]):
            with self.subTest(length=len(candidate)), self.assertRaises(ValueError):
                self.verify_events(candidate)

    def test_unknown_tests_and_events_after_package_completion_are_rejected(self):
        events = self.raw_events()
        with self.assertRaises(ValueError):
            self.verify_events(events[:2] + [dict(events[2], Test=verdict.ROOT + "/foreign")] + events[2:])
        with self.assertRaises(ValueError):
            self.verify_events(events + [events[0]])

    def test_v1_partial_foreign_and_reused_workload_artifacts_are_rejected(self):
        for change in ("v1", "old_bases", "missing_source", "foreign", "reused_layer", "reused_source", "boolean_version", "secret", "revision"):
            with self.subTest(change=change), self.assertRaises(ValueError):
                events = self.raw_events()
                event = self.identity_event(events)
                identity = json.loads(event["Output"].split(verdict.IDENTITY_PREFIX, 1)[1])
                manifest = identity["runtime"]
                if change == "v1":
                    manifest["version"] = 1
                elif change == "old_bases":
                    for name in ("deploy_base", "probe_app_layer", "node22_app_layer", "probe_source_archive", "node22_source_archive"):
                        del manifest["artifacts"][name]
                    manifest["artifacts"].update(probe_base="c" * 64, node22_base="d" * 64)
                elif change == "missing_source":
                    del manifest["artifacts"]["node22_source_archive"]
                elif change == "foreign":
                    manifest["artifacts"]["foreign"] = "c" * 64
                elif change == "reused_layer":
                    manifest["artifacts"]["node22_app_layer"] = manifest["artifacts"]["probe_app_layer"]
                elif change == "reused_source":
                    manifest["artifacts"]["node22_source_archive"] = manifest["artifacts"]["probe_source_archive"]
                elif change == "boolean_version":
                    manifest["version"] = True
                elif change == "secret":
                    manifest["provider_token"] = "private"
                else:
                    manifest["source_revision"] = "dirty"
                event["Output"] = verdict.IDENTITY_PREFIX + json.dumps(identity) + "\n"
                data = "\n".join(json.dumps(item) for item in events).encode()
                verdict.make_report(data, "native", True, True, True, 86400)

    def test_report_parameters_require_clean_source_valid_host_and_bounded_ttl(self):
        data = "\n".join(self.events()).encode()
        for host, clean, ttl in (("unknown", True, 86400), ("native", False, 86400),
                                 ("native", True, True), ("native", True, 0), ("native", True, 86400.0)):
            with self.subTest(host=host, clean=clean, ttl=ttl), self.assertRaises(ValueError):
                verdict.make_report(data, host, clean, True, True, ttl)
        self.assertEqual(verdict.make_report(data, "nested-diagnostic", True, True, True, 86400)["host_class"],
                         "nested-diagnostic")

    def test_report_publication_preserves_existing_files_and_symlinks(self):
        report = verdict.make_report("\n".join(self.events()).encode(), "native", True, True, True, 86400)
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "report.json"
            verdict.write_report(path, report)
            original = path.read_bytes()
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
            link = Path(directory) / "link.json"
            link.symlink_to(path)
            for target in (path, link):
                with self.assertRaises(FileExistsError):
                    verdict.write_report(target, report)
                self.assertEqual(path.read_bytes(), original)
            self.assertFalse(list(Path(directory).glob(".guest-evidence-*")))

    def test_event_timestamps_cannot_reverse_within_the_execution(self):
        events = self.raw_events()
        events[5]["Time"] = events[2]["Time"]
        with self.assertRaises(ValueError):
            verdict.verify(json.dumps(event) for event in events)


    def test_python_phases_and_artifacts_are_mandatory(self):
        events = self.raw_events()
        without_python = [event for event in events if "/python_starter_" not in event.get("Test", "")]
        with self.assertRaises(ValueError):
            self.verify_events(without_python)
        for artifact in ("python312_app_layer", "python312_source_archive"):
            with self.subTest(artifact=artifact), self.assertRaises(ValueError):
                events = self.raw_events()
                event = self.identity_event(events)
                identity = json.loads(event["Output"].split(verdict.IDENTITY_PREFIX, 1)[1])
                del identity["runtime"]["artifacts"][artifact]
                event["Output"] = verdict.IDENTITY_PREFIX + json.dumps(identity) + "\n"
                verdict.make_report("\n".join(json.dumps(item) for item in events).encode(), "native", True, True, True, 86400)

    def test_v2_and_renamed_probe_or_node_artifacts_cannot_qualify_python(self):
        for change in ("version", "probe_app_layer", "node22_app_layer", "probe_source_archive", "node22_source_archive"):
            with self.subTest(change=change), self.assertRaises(ValueError):
                events = self.raw_events()
                event = self.identity_event(events)
                identity = json.loads(event["Output"].split(verdict.IDENTITY_PREFIX, 1)[1])
                if change == "version":
                    identity["runtime"]["version"] = 2
                else:
                    target = "python312_app_layer" if change.endswith("app_layer") else "python312_source_archive"
                    identity["runtime"]["artifacts"][target] = identity["runtime"]["artifacts"][change]
                event["Output"] = verdict.IDENTITY_PREFIX + json.dumps(identity) + "\n"
                verdict.make_report("\n".join(json.dumps(item) for item in events).encode(), "native", True, True, True, 86400)


if __name__ == "__main__":
    unittest.main()
