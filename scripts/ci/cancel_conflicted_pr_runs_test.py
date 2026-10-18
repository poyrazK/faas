#!/usr/bin/env python3
"""Unit tests for cancel_conflicted_pr_runs.py."""
import importlib.util
import pathlib
import unittest

spec = importlib.util.spec_from_file_location(
    "cancel_conflicted_pr_runs", pathlib.Path(__file__).with_name("cancel_conflicted_pr_runs.py"))
ccr = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ccr)

REPO = "o/r"


def run(run_id, number=None, branch="b", head_repo=REPO, status="queued", name="ci-light"):
    return {"id": run_id, "name": name, "status": status, "head_branch": branch,
            "head_repository": {"full_name": head_repo},
            "pull_requests": [{"number": number}] if number else []}


class FakeAPI:
    def __init__(self, runs, pulls, open_list=(), fail_cancel=()):
        self.runs, self.pulls, self.open_list = runs, pulls, list(open_list)
        self.fail_cancel = set(fail_cancel)
        self.cancelled, self.pull_reads = [], {}

    def __call__(self, path, method="GET"):
        if path.startswith(f"repos/{REPO}/actions/runs?"):
            status = path.split("status=")[1].split("&")[0]
            page = int(path.split("page=")[-1])
            items = [r for r in self.runs if r["status"] == status] if page == 1 else []
            return {"workflow_runs": items}
        if path.startswith(f"repos/{REPO}/pulls?"):
            return self.open_list if path.endswith("page=1") else []
        if path.startswith(f"repos/{REPO}/pulls/"):
            number = int(path.rsplit("/", 1)[1])
            self.pull_reads[number] = self.pull_reads.get(number, 0) + 1
            states = self.pulls[number]
            return states[min(self.pull_reads[number], len(states)) - 1]
        if path.endswith("/cancel") and method == "POST":
            run_id = int(path.split("/")[-2])
            if run_id in self.fail_cancel:
                raise RuntimeError("Cannot cancel a workflow run that is completed.")
            self.cancelled.append(run_id)
            return {}
        raise AssertionError(f"unexpected call {method} {path}")


CLEAN = {"state": "open", "mergeable": True, "mergeable_state": "clean"}
DIRTY = {"state": "open", "mergeable": False, "mergeable_state": "dirty"}
UNKNOWN = {"state": "open", "mergeable": None, "mergeable_state": "unknown"}


class SweepTest(unittest.TestCase):
    def sweep(self, api, dry_run=False):
        return ccr.sweep(api, REPO, dry_run=dry_run, sleep=lambda s: None, log=lambda m: None)

    def test_cancels_only_runs_of_conflicting_prs(self):
        api = FakeAPI([run(1, 10), run(2, 10, status="in_progress"), run(3, 11)], {10: [DIRTY], 11: [CLEAN]})
        self.assertEqual(self.sweep(api), [1, 2])
        self.assertEqual(api.cancelled, [1, 2])

    def test_dry_run_cancels_nothing(self):
        api = FakeAPI([run(1, 10)], {10: [DIRTY]})
        self.assertEqual(self.sweep(api, dry_run=True), [1])
        self.assertEqual(api.cancelled, [])

    def test_unknown_mergeability_is_retried_then_left_alone(self):
        api = FakeAPI([run(1, 10), run(2, 11)], {10: [UNKNOWN, UNKNOWN, DIRTY], 11: [UNKNOWN] * 5})
        self.assertEqual(self.sweep(api), [1])
        self.assertEqual(api.pull_reads[11], 4)

    def test_closed_pr_and_blocked_but_mergeable_pr_are_not_conflicts(self):
        api = FakeAPI([run(1, 10), run(2, 11)], {
            10: [{"state": "closed", "mergeable": False, "mergeable_state": "dirty"}],
            11: [{"state": "open", "mergeable": True, "mergeable_state": "blocked"}]})
        self.assertEqual(self.sweep(api), [])

    def test_fork_run_maps_to_its_pr_by_head(self):
        fork = run(1, None, branch="feature", head_repo="someone/r")
        open_list = [{"number": 12, "head": {"ref": "feature", "repo": {"full_name": "someone/r"}}},
                     {"number": 13, "head": {"ref": "feature", "repo": {"full_name": REPO}}}]
        api = FakeAPI([fork], {12: [DIRTY], 13: [DIRTY]}, open_list=open_list)
        self.assertEqual(self.sweep(api), [1])

    def test_run_that_finished_meanwhile_is_skipped(self):
        api = FakeAPI([run(1, 10), run(2, 10)], {10: [DIRTY]}, fail_cancel=[1])
        self.assertEqual(self.sweep(api), [2])

    def test_no_active_runs_reads_no_pull_requests(self):
        api = FakeAPI([], {})
        self.assertEqual(self.sweep(api), [])
        self.assertEqual(api.pull_reads, {})


if __name__ == "__main__":
    unittest.main()
