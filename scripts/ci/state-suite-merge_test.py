"""ADR-570: coverage must reject omitted partitions or inconsistent inventories."""
import copy
import hashlib
import importlib.util
import json
import os
import pathlib
import sys
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location("merge_suite", pathlib.Path(__file__).with_name("state-suite-merge.py"))
merge = importlib.util.module_from_spec(spec)
spec.loader.exec_module(merge)


class MergeTest(unittest.TestCase):
    def receipts(self):
        groups = merge.suite.partitions([f"Test{index}" for index in range(16)], 8)
        return [{"partition": index, "partition_count": 8, "commit": "source", "packages": ["state", "others"],
                 "groups": groups, "go_version": "pinned", "state_runtime_environment": {"GREGALE_GITOPS_ACCEPTANCE": "1"},
                 "source_unchanged": True, "result": "passed"} for index in range(8)]

    def test_accepts_complete_disjoint_inventory(self):
        receipts = self.receipts()
        self.assertEqual(merge.verify_inventory(receipts), receipts[0]["groups"])

    def test_all_partitions_must_enable_gitops_postgres_acceptance(self):
        for environment in [{}, {"GREGALE_GITOPS_ACCEPTANCE": "0"}]:
            receipts = self.receipts()
            for item in receipts:
                item["state_runtime_environment"] = environment
            with self.assertRaises(ValueError):
                merge.verify_inventory(receipts)

    def test_refuses_missing_duplicate_failed_or_changed_partition(self):
        original = self.receipts()
        variants = [original[:-1]]
        for key, value in [
            ("partition", 0), ("source_unchanged", False), ("result", "failed"),
            ("commit", "different"), ("groups", [["TestMissing"]]),
            ("packages", ["state", "others", "unexpected"]), ("go_version", "changed"),
            ("state_runtime_environment", {"GOMAXPROCS": "different"}),
        ]:
            altered = copy.deepcopy(original)
            altered[-1][key] = value
            variants.append(altered)
        for variant in variants:
            with self.assertRaises(ValueError):
                merge.verify_inventory(variant)

    def complete_artifacts(self, directory):
        artifacts = directory / "artifacts"
        freeze = {"commit": "source", "files": {"fixture": "unchanged"}}
        receipts = self.receipts()
        names = sorted(name for group in receipts[0]["groups"] for name in group)
        packages = ["github.com/onebox-faas/faas/pkg/state", "github.com/onebox-faas/faas/pkg/state/conformance"]
        for index, receipt in enumerate(receipts):
            folder = artifacts / str(index)
            folder.mkdir(parents=True)
            producer = pathlib.Path("/runner/coverage/state-suite") / str(index)
            binary = b"fixture binary, never executed"
            (folder / "state.test").write_bytes(binary)
            (folder / "source-freeze.json").write_text(json.dumps(freeze))
            (folder / "inventory.log").write_text("\n".join(names) + "\n")
            (folder / "state.log").write_text("".join(f"--- PASS: {name} (0s)\n" for name in receipt["groups"][index]))
            (folder / "state.out").write_text("mode: atomic\nstate.go:1.1,2.1 3 1\n")
            receipt.update(packages=packages, go_version="go version go1.25.13 linux/amd64",
                           binary_sha256=hashlib.sha256(binary).hexdigest())
            receipt["commands"] = [
                {"args": ["go", "test", "-race", "-c", "-p=1", "-cover", "-covermode=atomic", "-o", str(producer / "state.test"), packages[0]], "exit_code": 0},
                {"args": [str(producer / "state.test"), "-test.v", "-test.count=1", "-test.timeout=30m",
                          "-test.run=^(" + "|".join(receipt["groups"][index]) + ")$", "-test.coverprofile=" + str(producer / "state.out")],
                 "exit_code": 0, "cwd": "/runner/pkg/state"},
            ]
            if index == 0:
                receipt["commands"].append({"args": ["go", "test", "-v", "-race", "-count=1", "-p=4", "-timeout=30m", "-covermode=atomic",
                                                    merge.suite.parity_coverage_arg(packages[1:]), "-coverprofile=" + str(producer / "others.out"), *packages[1:]], "exit_code": 0})
                (folder / "others.out").write_text("mode: atomic\nother.go:1.1,2.1 2 1\n")
            (folder / "terminal.json").write_text(json.dumps(receipt))
        return artifacts, freeze

    def aggregate(self, directory, artifacts, freeze):
        coverage = directory / "coverage" / "complete.out"
        with mock.patch.object(sys, "argv", ["merge", "--artifacts", str(artifacts), "--coverage", str(coverage)]), \
             mock.patch.dict(os.environ, {"GITHUB_SHA": freeze["commit"]}), \
             mock.patch.object(merge.checks, "source_snapshot", return_value=freeze):
            merge.main()
        return coverage

    def test_aggregates_verbose_remaining_package_receipt(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = pathlib.Path(temporary)
            artifacts, freeze = self.complete_artifacts(directory)
            coverage = self.aggregate(directory, artifacts, freeze)
            self.assertEqual(coverage.read_text(), "mode: atomic\nother.go:1.1,2.1 2 1\nstate.go:1.1,2.1 3 8\n")
            verdict = json.loads((coverage.parent / "state-coverage-terminal.json").read_text())
            self.assertEqual((verdict["result"], verdict["roots"], verdict["partitions"]), ("passed", 16, 8))
            self.assertTrue(verdict["source_unchanged"])

    def test_aggregation_refuses_altered_remaining_package_execution(self):
        mutations = {
            "missing verbose": lambda args: args.remove("-v"),
            "missing race": lambda args: args.remove("-race"),
            "repeat count": lambda args: args.__setitem__(4, "-count=2"),
            "parallel limit": lambda args: args.__setitem__(5, "-p=1"),
            "deadline": lambda args: args.__setitem__(6, "-timeout=60m"),
            "coverage mode": lambda args: args.__setitem__(7, "-covermode=set"),
            "instrumented scope": lambda args: args.__setitem__(8, "-coverpkg=github.com/onebox-faas/faas/pkg/state"),
            "profile path": lambda args: args.__setitem__(9, "-coverprofile=/runner/unrelated.out"),
            "missing package": lambda args: args.pop(),
            "extra package": lambda args: args.append("github.com/onebox-faas/faas/pkg/unrelated"),
            "test filter": lambda args: args.insert(10, "-run=TestOnlyOne"),
        }
        for name, mutate in mutations.items():
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temporary:
                directory = pathlib.Path(temporary)
                artifacts, freeze = self.complete_artifacts(directory)
                terminal = artifacts / "0" / "terminal.json"
                receipt = json.loads(terminal.read_text())
                mutate(receipt["commands"][2]["args"])
                terminal.write_text(json.dumps(receipt))
                with self.assertRaisesRegex(ValueError, "remaining package scope or flags changed"):
                    self.aggregate(directory, artifacts, freeze)
                self.assertFalse((directory / "coverage" / "complete.out").exists())
                verdict = json.loads((directory / "coverage" / "state-coverage-terminal.json").read_text())
                self.assertEqual(verdict["result"], "failed")


if __name__ == "__main__":
    unittest.main()
