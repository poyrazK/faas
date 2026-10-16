#!/usr/bin/env python3
import copy
import datetime as dt
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("verdict", Path(__file__).with_name("object-storage-native-verdict.py"))
verdict = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verdict)


class VerdictTests(unittest.TestCase):
    def events(self):
        events = []
        start = dt.datetime(2026, 10, 15, tzinfo=dt.UTC)

        def emit(name, action, output=None):
            event = dict(Package=verdict.PACKAGE, Action=action,
                         Time=(start + dt.timedelta(seconds=len(events))).isoformat())
            if name:
                event["Test"] = name
            if output is not None:
                event["Output"] = output + "\n"
            events.append(event)

        emit("", "start")
        emit(verdict.ROOT, "run")
        for index, starter in enumerate(verdict.STARTERS):
            name = verdict.ROOT + "/" + starter["template"]
            emit(name, "run")
            for phase in verdict.PHASES:
                emit(name + "/" + phase, "run")
                emit(name + "/" + phase, "pass")
            identity = dict(version=verdict.CONTRACT["version"], source_revision="a" * 40,
                            template=starter["template"], runtime=starter["runtime"],
                            artifacts={name: "b" * 64 for name in (*verdict.CONTRACT["artifacts"], starter["app_artifact"], starter["source_artifact"])})
            identity["artifacts"][starter["app_artifact"]] = str(index + 1) * 64
            identity["artifacts"][starter["source_artifact"]] = str(index + 3) * 64
            emit(name, "output", "native_test.go:1: " + verdict.IDENTITY_PREFIX + json.dumps(identity))
            emit(name, "pass")
        emit(verdict.ROOT, "pass")
        emit("", "pass")
        return events

    def verify(self, events):
        # Reindex times so mutations exercise ordering rather than timestamps.
        start = dt.datetime(2026, 10, 15, tzinfo=dt.UTC)
        return verdict.verify(json.dumps(dict(event, Time=(start + dt.timedelta(seconds=index)).isoformat()))
                              for index, event in enumerate(events))

    def report(self, events=None, host_class="native", clean=True, before=True, after=True, ttl=86400):
        data = "\n".join(json.dumps(event) for event in (self.events() if events is None else events)).encode()
        return verdict.make_report(data, host_class, clean, before, after, ttl)

    def identity_event(self, events, starter):
        return next(event for event in events if event.get("Test") == verdict.ROOT + "/" + starter["template"]
                    and verdict.IDENTITY_PREFIX in event.get("Output", ""))

    def identity(self, event):
        return json.loads(event["Output"].split(verdict.IDENTITY_PREFIX, 1)[1])

    def test_complete_native_and_nested_reports_are_distinct(self):
        report = self.report()
        self.assertTrue(report["native_qualified"])
        self.assertEqual(report["version"], 2)
        self.assertEqual(report["source_revision"], "a" * 40)
        self.assertEqual([journey["template"] for journey in report["journeys"]],
                         ["object-storage-node", "object-storage-python"])
        for journey, starter in zip(report["journeys"], verdict.STARTERS, strict=True):
            self.assertEqual(journey["runtime"], starter["runtime"])
            self.assertEqual(len(journey["checks"]), len(verdict.PHASES))
            self.assertEqual(set(journey["identity"]["artifacts"]), {*verdict.CONTRACT["artifacts"], starter["app_artifact"], starter["source_artifact"]})
        self.assertFalse(self.report(host_class="nested-diagnostic")["native_qualified"])
        self.assertNotIn("Output", json.dumps(report))

    def test_all_phases_starters_and_parent_are_mandatory(self):
        names = [verdict.ROOT]
        for starter in verdict.STARTERS:
            name = verdict.ROOT + "/" + starter["template"]
            names.extend((name, *(name + "/" + phase for phase in verdict.PHASES)))
        for name in names:
            with self.subTest(name=name), self.assertRaises(ValueError):
                self.verify([event for event in self.events() if event.get("Test") != name])

    def test_single_runtime_evidence_cannot_qualify_the_matrix(self):
        for starter in verdict.STARTERS:
            prefix = verdict.ROOT + "/" + starter["template"]
            with self.subTest(starter=starter["template"]), self.assertRaises(ValueError):
                self.verify([event for event in self.events()
                             if event.get("Test") != prefix and not event.get("Test", "").startswith(prefix + "/")])

    def test_skipped_failed_and_foreign_package_failure_are_rejected(self):
        for action in ("skip", "fail", "build-fail"):
            with self.subTest(action=action), self.assertRaises(ValueError):
                events = self.events()
                events[4]["Action"] = action
                self.verify(events)
        with self.assertRaises(ValueError):
            self.verify(self.events() + [dict(Action="fail", Package="dependency")])

    def test_pass_requires_run_and_each_phase_can_run_only_once(self):
        events = self.events()
        for candidate in (events[:3] + events[4:], events[:5] + [events[4]] + events[5:],
                          events[:5] + [events[3]] + events[5:]):
            with self.subTest(candidate=candidate[:5]), self.assertRaises(ValueError):
                self.verify(candidate)

    def test_phases_cannot_overlap_or_change_order(self):
        events = self.events()
        with self.assertRaises(ValueError):
            self.verify(events[:3] + events[5:7] + events[3:5] + events[7:])
        with self.assertRaises(ValueError):
            self.verify(events[:4] + [events[5]] + events[4:5] + events[6:])

    def test_starter_journeys_cannot_overlap_or_change_order(self):
        events = self.events()
        boundary = next(index for index, event in enumerate(events)
                        if event.get("Action") == "run" and event.get("Test") == verdict.ROOT + "/" + verdict.STARTERS[1]["template"])
        with self.assertRaises(ValueError):
            self.verify(events[:2] + events[boundary:-2] + events[2:boundary] + events[-2:])
        with self.assertRaises(ValueError):
            self.verify(events[:3] + [events[boundary]] + events[3:boundary] + events[boundary + 1:])

    def test_each_post_cleanup_identity_is_mandatory_and_cannot_be_duplicated(self):
        for starter in verdict.STARTERS:
            events = self.events()
            identity = self.identity_event(events, starter)
            index = events.index(identity)
            for candidate in (events[:index] + events[index + 1:],
                              events[:3] + [identity] + events[3:index] + events[index + 1:],
                              events[:index] + [identity, identity] + events[index + 1:],
                              events[:index] + events[index + 1:index + 2] + [identity] + events[index + 2:]):
                with self.subTest(starter=starter["template"]), self.assertRaises(ValueError):
                    self.verify(candidate)
        with self.assertRaises(ValueError):
            self.verify(self.events()[:-1])

    def test_report_requires_clean_source_and_both_leak_checks(self):
        for flags in (dict(clean=False), dict(before=False), dict(after=False),
                      dict(host_class="virtual"), dict(ttl=0), dict(ttl=86401)):
            with self.subTest(flags=flags), self.assertRaises(ValueError):
                self.report(**flags)

    def test_partial_foreign_or_secret_bearing_runtime_identity_is_rejected(self):
        for starter in verdict.STARTERS:
            for change in ("missing_layer", "missing_source", "other_layer", "extra_artifact", "secret", "revision", "digest", "boolean_version", "old_version", "template", "runtime"):
                with self.subTest(starter=starter["template"], change=change), self.assertRaises(ValueError):
                    events = self.events()
                    event = self.identity_event(events, starter)
                    identity = self.identity(event)
                    if change in ("missing_layer", "other_layer"):
                        del identity["artifacts"][starter["app_artifact"]]
                        if change == "other_layer":
                            other = next(item for item in verdict.STARTERS if item != starter)
                            identity["artifacts"][other["app_artifact"]] = "b" * 64
                    elif change == "extra_artifact":
                        identity["artifacts"]["foreign"] = "c" * 64
                    elif change == "missing_source":
                        del identity["artifacts"][starter["source_artifact"]]
                    elif change == "secret":
                        identity["secret"] = "must never be exported"
                    elif change == "revision":
                        identity["source_revision"] = "dirty"
                    elif change == "boolean_version":
                        identity["version"] = True
                    elif change == "old_version":
                        identity["version"] = 1
                    elif change in ("template", "runtime"):
                        identity[change] = "foreign"
                    else:
                        identity["artifacts"][starter["app_artifact"]] = "invalid"
                    event["Output"] = verdict.IDENTITY_PREFIX + json.dumps(identity) + "\n"
                    self.report(events)

    def test_node_identity_cannot_be_reused_for_python(self):
        events = self.events()
        first, second = [self.identity_event(events, starter) for starter in verdict.STARTERS]
        second["Output"] = first["Output"]
        with self.assertRaises(ValueError):
            self.report(events)

    def test_both_journeys_must_use_the_same_clean_revision_and_shared_artifacts(self):
        for change in ("source_revision", *verdict.CONTRACT["artifacts"]):
            with self.subTest(change=change), self.assertRaises(ValueError):
                events = self.events()
                event = self.identity_event(events, verdict.STARTERS[1])
                identity = self.identity(event)
                if change == "source_revision":
                    identity[change] = "c" * 40
                else:
                    identity["artifacts"][change] = "c" * 64
                event["Output"] = verdict.IDENTITY_PREFIX + json.dumps(identity) + "\n"
                self.report(events)

    def test_runtime_artifacts_cannot_be_reused_under_other_starter_names(self):
        for field in ("app_artifact", "source_artifact"):
            with self.subTest(field=field), self.assertRaises(ValueError):
                events = self.events()
                first, second = [self.identity_event(events, starter) for starter in verdict.STARTERS]
                identity = self.identity(second)
                identity["artifacts"][verdict.STARTERS[1][field]] = self.identity(first)["artifacts"][verdict.STARTERS[0][field]]
                second["Output"] = verdict.IDENTITY_PREFIX + json.dumps(identity) + "\n"
                self.report(events)

    def test_distinct_deployed_layers_are_allowed(self):
        events = self.events()
        starter = verdict.STARTERS[1]
        event = self.identity_event(events, starter)
        identity = self.identity(event)
        identity["artifacts"][starter["app_artifact"]] = "c" * 64
        event["Output"] = verdict.IDENTITY_PREFIX + json.dumps(identity) + "\n"
        self.assertTrue(self.report(events)["native_qualified"])

    def test_fragmented_go_test_cleanup_identity_is_accepted_for_both_starters(self):
        events = self.events()
        for starter in verdict.STARTERS:
            event = self.identity_event(events, starter)
            index = events.index(event)
            output = event["Output"]
            chunks = [dict(event, Output=output[offset:offset + 256]) for offset in range(0, len(output), 256)]
            events[index:index + 1] = chunks
        self.assertTrue(self.report(events)["native_qualified"])

    def test_incomplete_interrupted_or_oversized_identity_fragments_are_rejected(self):
        for change in ("incomplete", "interrupted", "oversized", "duplicate_prefix"):
            with self.subTest(change=change), self.assertRaises(ValueError):
                events = self.events()
                event = self.identity_event(events, verdict.STARTERS[0])
                index = events.index(event)
                output = event["Output"]
                event["Output"] = output[:120]
                if change == "incomplete":
                    pass
                elif change == "oversized":
                    events.insert(index + 1, dict(event, Output="x" * verdict.MAX_IDENTITY_BYTES))
                elif change == "duplicate_prefix":
                    events.insert(index + 1, dict(event, Output=output))
                else:
                    events.insert(index + 1, dict(event, Test=verdict.ROOT, Output=output[120:]))
                self.verify(events)

    def test_empty_invalid_and_wrong_suite_evidence_is_rejected(self):
        for lines in ([], ["garbage"], ["[]"]):
            with self.subTest(lines=lines), self.assertRaises(ValueError):
                verdict.verify(lines)
        with self.assertRaises(ValueError):
            self.verify([dict(event, Package="foreign") for event in self.events()])

    def test_invalid_or_reversed_timestamps_are_rejected(self):
        for when in ("invalid", "2026-10-14T00:00:00Z", "2026-10-15T00:00:00"):
            with self.subTest(when=when), self.assertRaises(ValueError):
                events = self.events()
                events[4]["Time"] = when
                self.report(events)
        events = self.events()
        events[5]["Time"] = events[2]["Time"]
        with self.assertRaises(ValueError):
            self.report(events)

    def test_events_after_package_pass_and_old_single_starter_shape_are_rejected(self):
        with self.assertRaises(ValueError):
            self.verify(self.events() + [copy.deepcopy(self.events()[0])])
        events = self.events()
        for event in events:
            if "Test" in event:
                event["Test"] = event["Test"].replace("/object-storage-node", "")
        with self.assertRaises(ValueError):
            self.verify(events)

    def test_report_never_overwrites_existing_evidence_or_symlinks(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "report.json"
            verdict.write_report(path, self.report())
            first = path.read_bytes()
            with self.assertRaises(FileExistsError):
                verdict.write_report(path, self.report())
            self.assertEqual(path.read_bytes(), first)
            link = Path(directory) / "link.json"
            link.symlink_to(path)
            with self.assertRaises(FileExistsError):
                verdict.write_report(link, self.report())
            self.assertEqual(path.read_bytes(), first)
            self.assertFalse(list(Path(directory).glob(".storage-evidence-*")))


if __name__ == "__main__":
    unittest.main()
