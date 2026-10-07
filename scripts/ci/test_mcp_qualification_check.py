import importlib.util
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("qualification", Path(__file__).with_name("mcp-qualification-check.py"))
qualification = importlib.util.module_from_spec(spec)
spec.loader.exec_module(qualification)


class QualificationGateTest(unittest.TestCase):
    def test_incomplete_and_changed_evidence_cannot_pass(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            artifact = root / "receipt.json"
            artifact.write_text('{"observed":"fixture"}')
            manifest = root / "manifest.json"
            commit = "a" * 40
            records = [{"name": name, "status": "passed", "target": "test-only-fixture",
                        "artifact": artifact.name, "sha256": hashlib.sha256(artifact.read_bytes()).hexdigest()}
                       for name in sorted(qualification.REQUIRED)]
            evidence = {"version": 1, "commit": commit, "checks": records}
            manifest.write_text(json.dumps(evidence))
            self.assertTrue(qualification.check(manifest, commit)["ok"])
            for mutation in ("missing", "pending", "source", "digest", "escape"):
                changed = json.loads(json.dumps(evidence))
                if mutation == "missing": changed["checks"].pop()
                if mutation == "pending": changed["checks"][0]["status"] = "pending"
                if mutation == "source": changed["commit"] = "b" * 40
                if mutation == "digest": changed["checks"][0]["sha256"] = "0" * 64
                if mutation == "escape": changed["checks"][0]["artifact"] = "../receipt.json"
                manifest.write_text(json.dumps(changed))
                with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                    qualification.check(manifest, commit)


if __name__ == "__main__":
    unittest.main()
