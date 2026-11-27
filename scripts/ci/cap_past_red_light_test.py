#!/usr/bin/env python3
"""Unit tests for cap_past_red_light.py."""
import importlib.util
import pathlib
import unittest

HERE = pathlib.Path(__file__).parent
spec = importlib.util.spec_from_file_location("cap_past_red_light", HERE / "cap_past_red_light.py")
cp = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cp)


def run(conclusion, status="completed"):
    return {"status": status, "conclusion": conclusion}


class FakeGitHub:
    def __init__(self, light):
        self.light, self.asked = light, []

    def get(self, path, params=None):
        assert path == f"actions/workflows/{cp.dp.LIGHT_WORKFLOW}/runs" and params["event"] == "push"
        self.asked.append(params["head_sha"])
        return {"workflow_runs": self.light.get(params["head_sha"], [])}


class PastRedLightTest(unittest.TestCase):
    def test_a_green_capped_commit_stays(self):
        gh = FakeGitHub({"c5": [run("success")], "c6": [run("success")]})
        self.assertEqual(cp.past_red_light(gh, ["c5", "c6"]), "c5")
        self.assertEqual(gh.asked, ["c5"])

    def test_a_red_capped_commit_moves_to_the_first_later_one_that_did_not_fail(self):
        # Deploy freeze 2026-11-26: the cap landed on 174890e8e5, whose light
        # tier failed; a later commit fixed it and every run picked the same red one.
        gh = FakeGitHub({"c5": [run("failure")], "c6": [run("timed_out"), run("success")],
                         "c7": [run("cancelled")], "c8": [run("success")]})
        self.assertEqual(cp.past_red_light(gh, ["c5", "c6", "c7", "c8"]), "c7")

    def test_a_running_or_unstarted_light_tier_is_not_red(self):
        gh = FakeGitHub({"c5": [run("failure")], "c6": [run(None, status="in_progress")]})
        self.assertEqual(cp.past_red_light(gh, ["c5", "c6"]), "c6")
        self.assertEqual(cp.past_red_light(FakeGitHub({"c5": [run("failure")]}), ["c5", "c6"]), "c6")

    def test_all_red_keeps_the_capped_commit(self):
        gh = FakeGitHub({"c5": [run("failure")], "c6": [run("failure")]})
        self.assertEqual(cp.past_red_light(gh, ["c5", "c6"]), "c5")

    def test_without_a_token_it_keeps_the_capped_commit(self):
        import contextlib
        import io
        import os
        out = io.StringIO()
        saved = {k: os.environ.pop(k, None) for k in ("GH_TOKEN", "GITHUB_TOKEN")}
        try:
            with contextlib.redirect_stdout(out):
                self.assertEqual(cp.main(["--repo", "o/r", "c5", "c6"]), 0)
        finally:
            for k, v in saved.items():
                if v is not None:
                    os.environ[k] = v
        self.assertEqual(out.getvalue().strip(), "c5")


if __name__ == "__main__":
    unittest.main()
