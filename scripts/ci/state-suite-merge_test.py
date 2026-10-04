"""ADR-570: coverage must reject omitted partitions or inconsistent inventories."""
import copy
import importlib.util
import pathlib
import unittest

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


if __name__ == "__main__":
    unittest.main()
