#!/usr/bin/env python3
import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("verdict", Path(__file__).with_name("managed-postgres-native-verdict.py"))
verdict = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verdict)


class VerdictTests(unittest.TestCase):
    def events(self):
        return [json.dumps({"Test": name, "Action": "pass"}) for name in
                (verdict.ROOT, *(verdict.ROOT + "/" + phase for phase in verdict.PHASES))]

    def test_complete(self):
        verdict.verify(self.events())

    def test_parent_pass_cannot_hide_skipped_or_failed_phase(self):
        for action in ("skip", "fail"):
            with self.subTest(action=action), self.assertRaises(ValueError):
                verdict.verify(self.events() + [json.dumps({"Test": verdict.ROOT + "/unexpected", "Action": action})])

    def test_omitted_phase(self):
        with self.assertRaises(ValueError):
            verdict.verify(self.events()[:-1])

    def test_package_failure_after_cleanup(self):
        with self.assertRaises(ValueError):
            verdict.verify(self.events() + [json.dumps({"Action": "fail"})])

    def test_empty_and_invalid_evidence(self):
        for lines in ([], ["garbage"]):
            with self.subTest(lines=lines), self.assertRaises(ValueError):
                verdict.verify(lines)


if __name__ == "__main__":
    unittest.main()
