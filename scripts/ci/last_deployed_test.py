#!/usr/bin/env python3
"""Unit tests for last_deployed.py."""
import importlib.util
import pathlib
import unittest

HERE = pathlib.Path(__file__).parent
spec = importlib.util.spec_from_file_location("last_deployed", HERE / "last_deployed.py")
ld = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ld)

CP = "Deploy the control-plane lane / Deploy unit release to Control Plane Host"
EDGE = "Deploy the edge lane / Roll the edge"


def job(name, conclusion="success", status="completed"):
    return {"name": name, "conclusion": conclusion, "status": status}


class FakeGitHub:
    def __init__(self, artifacts, jobs):
        self.artifacts, self.jobs_by_run = artifacts, jobs

    def get(self, path, params=None):
        if path == "actions/artifacts":
            page = params["page"]
            return {"artifacts": self.artifacts[(page - 1) * 100: page * 100]}
        run = int(path.split("/")[2])
        return {"jobs": self.jobs_by_run.get(run, [])}


def marker(sha, run, created, expired=False):
    return {"name": f"unit-release-sha-{sha}", "workflow_run": {"id": run}, "created_at": created, "expired": expired}


class LastDeployedTest(unittest.TestCase):
    def test_newest_run_whose_lanes_passed(self):
        gh = FakeGitHub(
            [marker("c3", 3, "2026-11-22T12:00"), marker("c2", 2, "2026-11-22T11:00"),
             {"name": "gregale-unit-release", "workflow_run": {"id": 9}, "created_at": "2026-11-22T13:00"},
             marker("c1", 1, "2026-11-22T10:00")],
            {3: [job(CP), job(EDGE, "failure")], 2: [job(CP), job(EDGE, "skipped")], 1: [job(CP)]})
        self.assertEqual(ld.last_deployed(gh), "c2")

    def test_expired_markers_and_no_passing_run(self):
        gh = FakeGitHub([marker("c2", 2, "2026-11-22T11:00", expired=True), marker("c1", 1, "2026-11-22T10:00")],
                        {2: [job(CP)], 1: [job(CP, "failure")]})
        self.assertIsNone(ld.last_deployed(gh))

    def test_a_run_with_no_lane_never_counts(self):
        gh = FakeGitHub([marker("c1", 1, "2026-11-22T10:00")], {1: [job("Check production can take a deploy")]})
        self.assertIsNone(ld.last_deployed(gh))


if __name__ == "__main__":
    unittest.main()
