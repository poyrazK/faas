#!/usr/bin/env python3
"""Unit tests for affected_packages.py package selection and sharding."""
import importlib.util
import io
import pathlib
import sys
import unittest
from unittest import mock

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
    "pkg/sched": {"path": f"{M}/pkg/sched", "imports": {f"{M}/pkg/api"}},
    "tests/property": {"path": f"{M}/tests/property", "imports": {f"{M}/pkg/sched"}},
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
        # tests/property depends on pkg/state transitively, so the §6.2
        # invariants run too.
        self.assertEqual(test, ["pkg/state", "tests/property"])

    def test_invariants_run_when_a_transitive_dependency_changes(self):
        # tests/property -> pkg/sched -> pkg/api -> pkg/state
        _, _, _, test = ap.select(["pkg/state/store.go"], PKGS)
        self.assertIn("tests/property", test)

    def test_invariants_skip_unrelated_changes(self):
        _, _, _, test = ap.select(["pkg/util/x.go"], PKGS)
        self.assertEqual(test, ["pkg/util"])

    def test_testdata_and_embedded_files_map_to_enclosing_package(self):
        _, changed, _, _ = ap.select(["./pkg/api/testdata/fixture.json"], PKGS)
        self.assertEqual(changed, ["pkg/api"])

    def test_migrations_are_vetted_but_tested_by_their_own_gate(self):
        _, changed, vet, test = ap.select(["migrations/20261009000000_x.sql"], PKGS)
        self.assertEqual(changed, ["migrations"])
        self.assertEqual(vet, ["migrations", "pkg/db"])
        # The migration gate in the checks job owns migrations' tests.
        self.assertEqual(test, [])

    def test_global_inputs_vet_everything_but_test_only_changed(self):
        everything, _, vet, test = ap.select(["go.sum", "pkg/util/x.go"], PKGS)
        self.assertTrue(everything)
        self.assertEqual(vet, sorted(PKGS))
        self.assertEqual(test, ["pkg/util", "tests/property"])

    def test_dotfile_paths_are_not_mangled(self):
        everything, _, _, _ = ap.select([".github/workflows/ci-light.yml"], PKGS)
        self.assertTrue(everything)


class ShardTest(unittest.TestCase):
    def test_shards_partition_the_set_deterministically(self):
        dirs = [f"pkg/p{i}" for i in range(20)] + ["cmd/gregale"]
        shards = [ap.shard(dirs, i, 3) for i in (1, 2, 3)]
        flat = sorted(d for s in shards for d in s)
        self.assertEqual(flat, sorted(dirs))
        self.assertEqual(shards, [ap.shard(dirs, i, 3) for i in (1, 2, 3)])

    def test_heaviest_package_gets_a_shard_to_itself(self):
        dirs = ["cmd/gregale", "pkg/x", "pkg/y"]
        shards = [ap.shard(dirs, i, 3) for i in (1, 2, 3)]
        self.assertIn(["cmd/gregale"], shards)

    def test_split_packages_are_never_assigned_whole(self):
        # The large API/state suites run by name across every shard.
        shards = [ap.shard(["cmd/apid", "pkg/state", "pkg/x"], i, 3) for i in (1, 2, 3)]
        self.assertEqual([d for s in shards for d in s], ["pkg/x"])
        self.assertIn("cmd/apid", ap.SPLIT_PACKAGES)
        self.assertIn("pkg/state", ap.SPLIT_PACKAGES)


class CommandTest(unittest.TestCase):
    def run_command(self, kind, paths, shard=1):
        output = io.StringIO()
        with mock.patch.object(ap, "go_packages", return_value=PKGS), \
                mock.patch.object(sys, "argv", ["affected_packages.py", kind, "--shard", str(shard), "--shards", "3"]), \
                mock.patch.object(sys, "stdin", io.StringIO("\n".join(paths))), \
                mock.patch.object(sys, "stdout", output):
            ap.main()
        return output.getvalue().split()

    def test_split_command_selects_only_affected_large_packages(self):
        for paths, want in [
            (["cmd/apid/handler_test.go"], ["./cmd/apid"]),
            (["pkg/state/store_test.go"], ["./pkg/state"]),
            (["cmd/apid/handler_test.go", "pkg/state/store_test.go"], ["./cmd/apid", "./pkg/state"]),
            (["pkg/util/helper.go"], []),
        ]:
            with self.subTest(paths=paths):
                self.assertEqual(self.run_command("split", paths), want)

    def test_standard_and_name_split_commands_cover_selected_packages_once(self):
        paths = ["cmd/apid/handler_test.go", "pkg/state/store_test.go", "pkg/util/helper.go"]
        ordinary = [p for i in (1, 2, 3) for p in self.run_command("test", paths, i)]
        large = self.run_command("split", paths)
        self.assertEqual(sorted(ordinary), ["./pkg/util", "./tests/property"])
        self.assertEqual(large, ["./cmd/apid", "./pkg/state"])
        self.assertEqual(len(ordinary + large), len(set(ordinary + large)))


if __name__ == "__main__":
    unittest.main()
