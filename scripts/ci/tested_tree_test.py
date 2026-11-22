#!/usr/bin/env python3
"""Unit tests for tested_tree.py (docs/adr/20261122170000-continuous-unit-lane.md)."""
import importlib.util
import pathlib
import unittest

HERE = pathlib.Path(__file__).parent
spec = importlib.util.spec_from_file_location("tested_tree", HERE / "tested_tree.py")
tt = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tt)

T, H = "t" * 40, "h" * 40
PULL = {"number": 7, "merge_commit_sha": T, "merged_at": "2026-11-22T10:00:00Z"}


def decide(pull=PULL, light=True, head=H, trees=None, ancestor=True, diffs=None):
    trees = trees or {T: "tree1", H: "tree1"}
    diffs = diffs or {(f"{T}^", T): ["pkg/apid/a.go"], (H, T): ["pkg/meter/m.go"]}
    return tt.decide(T, pull, light_ok=lambda h: light, fetch_head=lambda p: head,
                     tree=lambda s: trees[s], ancestor=lambda a, b: ancestor,
                     diff=lambda a, b: diffs.get((a, b), []))


class DecideTest(unittest.TestCase):
    def test_up_to_date_head_with_the_shipped_tree_and_green_light_is_tested(self):
        got = decide()
        self.assertEqual(got["verdict"], "tested")
        self.assertEqual(got["paths"], [])
        self.assertEqual((got["pull"], got["head"]), (7, H))

    def test_main_moved_tests_what_ci_did_not_see_together(self):
        got = decide(ancestor=False, trees={T: "tree2", H: "tree1"})
        self.assertEqual(got["verdict"], "run")
        self.assertIn("did not hold main's previous commit", got["reason"])
        self.assertEqual(got["paths"], ["pkg/apid/a.go", "pkg/meter/m.go"])

    def test_green_head_under_a_moved_main_tests_only_the_interaction(self):
        got = decide(ancestor=False, trees={T: "tree2", H: "tree1"})
        self.assertEqual(got["interaction"], {"own": ["pkg/apid/a.go"], "others": ["pkg/meter/m.go"]})
        self.assertIn("both its change and the merges since affect", got["reason"])

    def test_head_ci_did_not_pass_on_tests_every_affected_package(self):
        got = decide(ancestor=False, trees={T: "tree2", H: "tree1"}, light=False)
        self.assertNotIn("interaction", got)
        self.assertEqual(got["paths"], ["pkg/apid/a.go", "pkg/meter/m.go"])

    def test_select_takes_packages_both_sides_affect(self):
        graph = {"pkg/apid/a.go": ["./pkg/apid", "./cmd/apid"], "pkg/meter/m.go": ["./pkg/meter", "./cmd/apid"],
                 "pkg/wire/w.go": ["./pkg/wire", "./pkg/apid", "./pkg/meter", "./cmd/apid"]}

        def affected(paths, kind):
            return sorted({p for path in paths for p in graph[path]})
        cases = [
            ("disjoint changes meet in a shared dependent",
             {"interaction": {"own": ["pkg/apid/a.go"], "others": ["pkg/meter/m.go"]}}, ["./cmd/apid"]),
            ("a file both sides changed keeps all its packages",
             {"interaction": {"own": ["pkg/wire/w.go"], "others": ["pkg/wire/w.go"]}}, graph["pkg/wire/w.go"]),
            ("no other merge: nothing CI did not see",
             {"interaction": {"own": ["pkg/apid/a.go"], "others": []}}, []),
            ("without interaction every affected package",
             {"paths": ["pkg/apid/a.go", "pkg/meter/m.go"]}, ["./cmd/apid", "./pkg/apid", "./pkg/meter"]),
        ]
        for name, plan, want in cases:
            with self.subTest(name):
                self.assertEqual(tt.select(plan, "test", affected=affected), sorted(want))

    def test_same_ancestry_but_other_tree_runs(self):
        got = decide(trees={T: "tree2", H: "tree1"})
        self.assertEqual(got["verdict"], "run")
        self.assertIn("tree differs", got["reason"])

    def test_light_not_passed_runs(self):
        got = decide(light=False)
        self.assertEqual(got["verdict"], "run")
        self.assertIn("ci-light did not pass", got["reason"])

    def test_no_pull_or_unfetchable_head_tests_the_commit_alone(self):
        for kw, why in (({"pull": None}, "no merged pull request"), ({"head": None}, "cannot fetch")):
            with self.subTest(why=why):
                got = decide(**kw)
                self.assertEqual(got["verdict"], "run")
                self.assertIn(why, got["reason"])
                self.assertEqual(got["paths"], ["pkg/apid/a.go"])


class MergedPullTest(unittest.TestCase):
    def test_only_the_pull_this_commit_merged(self):
        class GH:
            def get(self, path, params=None):
                assert path == f"commits/{T}/pulls"
                return [{"number": 5, "merge_commit_sha": "x" * 40, "merged_at": "t"},
                        {"number": 6, "merge_commit_sha": T, "merged_at": None}, PULL]
        self.assertEqual(tt.merged_pull(GH(), T)["number"], 7)

    def test_normalise(self):
        self.assertEqual(tt.normalise("./pkg/apid"), "github.com/onebox-faas/faas/pkg/apid")
        self.assertEqual(tt.normalise("github.com/onebox-faas/faas/cmd/x"), "github.com/onebox-faas/faas/cmd/x")


if __name__ == "__main__":
    unittest.main()
