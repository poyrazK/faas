#!/usr/bin/env python3
"""Unit tests for affected_packages.py package selection and sharding."""
import importlib.util
import pathlib
import unittest

spec = importlib.util.spec_from_file_location(
    "affected_packages", pathlib.Path(__file__).with_name("affected_packages.py"))
ap = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ap)

M = ap.MODULE
PKGS = {
    "pkg/state": {"path": f"{M}/pkg/state", "imports": {"context"}},
    "pkg/api": {"path": f"{M}/pkg/api", "imports": {f"{M}/pkg/state"}},
    "cmd/apid": {"path": f"{M}/cmd/apid", "imports": {f"{M}/pkg/api"}},
    "pkg/util": {"path": f"{M}/pkg/util", "imports": set()},
    "cmd/e2e": {"path": f"{M}/cmd/e2e", "imports": {f"{M}/pkg/state"}},
    "migrations": {"path": f"{M}/migrations", "imports": set()},
    "pkg/db": {"path": f"{M}/pkg/db", "imports": {f"{M}/migrations"}},
}


class SelectTest(unittest.TestCase):
    def test_docs_only_selects_nothing(self):
        everything, changed, vet, test = ap.select(["docs/quickstart.md", "README.md"], PKGS)
        self.assertFalse(everything)
        self.assertEqual(changed, [])
        self.assertEqual(vet, [])
        self.assertEqual(test, [])

    def test_changed_packages_are_tested_and_direct_importers_vetted(self):
        _, changed, vet, test = ap.select(["pkg/state/store.go"], PKGS)
        self.assertEqual(changed, ["pkg/state"])
        # pkg/api imports pkg/state directly; cmd/apid only transitively.
        self.assertEqual(vet, ["cmd/e2e", "pkg/api", "pkg/state"])
        self.assertEqual(test, ["pkg/state"])

    def test_testdata_and_embedded_files_map_to_enclosing_package(self):
        _, changed, _, _ = ap.select(["./pkg/api/testdata/fixture.json"], PKGS)
        self.assertEqual(changed, ["pkg/api"])

    def test_migrations_are_vetted_but_tested_by_their_own_gate(self):
        _, changed, vet, test = ap.select(["migrations/20261009000000_x.sql"], PKGS)
        self.assertEqual(changed, ["migrations"])
        self.assertEqual(vet, ["migrations", "pkg/db"])
        # The migration gate in the checks job owns migrations' tests.
        self.assertEqual(test, [])

    def test_global_inputs_select_everything_except_dedicated_suites(self):
        everything, _, vet, test = ap.select(["go.sum"], PKGS)
        self.assertTrue(everything)
        self.assertEqual(vet, sorted(PKGS))
        self.assertEqual(test, ["cmd/apid", "pkg/api", "pkg/db", "pkg/state", "pkg/util"])

    def test_dotfile_paths_are_not_mangled(self):
        everything, _, _, _ = ap.select([".github/workflows/ci-light.yml"], PKGS)
        self.assertTrue(everything)


class ShardTest(unittest.TestCase):
    def test_shards_partition_the_set_deterministically(self):
        dirs = [f"pkg/p{i}" for i in range(20)] + ["cmd/apid"]
        shards = [ap.shard(dirs, i, 3) for i in (1, 2, 3)]
        flat = sorted(d for s in shards for d in s)
        self.assertEqual(flat, sorted(dirs))
        self.assertEqual(shards, [ap.shard(dirs, i, 3) for i in (1, 2, 3)])

    def test_heaviest_package_gets_a_shard_to_itself(self):
        dirs = ["cmd/apid", "pkg/x", "pkg/y"]
        shards = [ap.shard(dirs, i, 3) for i in (1, 2, 3)]
        self.assertIn(["cmd/apid"], shards)

    def test_split_packages_are_never_assigned_whole(self):
        # pkg/state runs by test name across every shard instead.
        shards = [ap.shard(["pkg/state", "pkg/x"], i, 3) for i in (1, 2, 3)]
        self.assertNotIn("pkg/state", [d for s in shards for d in s])
        self.assertIn("pkg/state", ap.SPLIT_PACKAGES)


if __name__ == "__main__":
    unittest.main()
