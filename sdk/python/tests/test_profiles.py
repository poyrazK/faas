"""Profile absence and recursive CPU stack wire compatibility."""

from faas_sdk.models import ProfileCompareRequest, ProfileFunctionDelta, ProfileResponse, ProfileStackDelta
from faas_sdk.types import UNSET


def test_missing_samples_and_recursive_tree_round_trip():
    query = {
        "deployment_id": "11111111-1111-4111-8111-111111111111",
        "runtime": "node24",
        "start": "2026-10-07T18:00:00Z",
        "end": "2026-10-07T18:01:00Z",
    }
    data = {
        "query": query,
        "cpu_seconds": 0,
        "stack_count": 0,
        "functions": [],
        "flamegraph": {"name": "all", "cpu_seconds": 0, "children": []},
        "empty": True,
    }
    missing = ProfileResponse.from_dict(data)
    assert missing.empty and not missing.functions
    assert ProfileResponse.from_dict(missing.to_dict()).flamegraph.children == []
    comparison = ProfileCompareRequest.from_dict({"baseline": query, "candidate": query})
    assert str(comparison.candidate.deployment_id) == query["deployment_id"]
    data["empty"] = False
    data["flamegraph"]["children"] = [{"name": "hot", "cpu_seconds": 1, "children": []}]
    loaded = ProfileResponse.from_dict(data)
    assert loaded.to_dict()["flamegraph"]["children"][0]["name"] == "hot"
    data["coverage"] = {
        "available": True,
        "received_profiles": 4,
        "contributing_collectors": 2,
        "window_seconds": 60,
        "covered_seconds": 20,
        "gap_seconds": 40,
        "recorded_failed_uploads": 1,
        "failures_complete": False,
        "last_received_at": "2026-10-07T18:01:00Z",
    }
    covered = ProfileResponse.from_dict(data)
    assert covered.coverage.received_profiles == 4
    assert not covered.coverage.failures_complete
    assert ProfileResponse.from_dict(covered.to_dict()).coverage.gap_seconds == 40
    data["source"] = {
        "available": True,
        "repository": "acme/service",
        "commit_sha": "a" * 40,
        "commit_url": "https://github.com/acme/service/commit/" + "a" * 40,
    }
    location = {
        "url": "https://github.com/acme/service/blob/" + "a" * 40 + "/server.py#L42",
        "path": "server.py",
        "line": 42,
    }
    data["functions"] = [
        {
            "name": "hot",
            "file": "/app/server.py",
            "line": 42,
            "self_cpu_seconds": 1,
            "total_cpu_seconds": 1,
            "source": location,
        }
    ]
    data["flamegraph"]["children"][0].update(file="/app/server.py", line=42, source=location)
    linked = ProfileResponse.from_dict(data)
    assert linked.source.available and linked.source.commit_sha == "a" * 40
    assert linked.functions[0].source.line == 42
    assert linked.flamegraph.children[0].source.url == location["url"]
    round_trip = ProfileResponse.from_dict(linked.to_dict())
    assert round_trip.functions[0].source.path == "server.py"


def test_differential_tree_preserves_unknown_rates_and_observation_flags():
    frame = {
        "name": "handler",
        "width_cpu_per_second": 0.5,
        "baseline_cpu_per_second": 0.2,
        "candidate_cpu_per_second": 0.3,
        "delta_cpu_per_second": 0.1,
        "children": [{"name": "newPath", "width_cpu_per_second": 0.3, "candidate_cpu_per_second": 0.3, "children": []}],
    }
    tree = ProfileStackDelta.from_dict(frame)
    assert tree.delta_cpu_per_second == 0.1
    assert tree.children[0].baseline_cpu_per_second is UNSET
    assert tree.children[0].delta_cpu_per_second is UNSET
    encoded = tree.to_dict()
    assert "baseline_cpu_per_second" not in encoded["children"][0]
    assert "delta_cpu_per_second" not in encoded["children"][0]
    zero = ProfileStackDelta.from_dict(
        {"name": "observedZero", "width_cpu_per_second": 0, "candidate_cpu_per_second": 0, "children": []}
    )
    assert zero.to_dict()["candidate_cpu_per_second"] == 0
    row = ProfileFunctionDelta.from_dict(
        {
            "name": "newPath",
            "baseline_cpu_per_second": 0,
            "candidate_cpu_per_second": 0.3,
            "delta_cpu_per_second": 0,
            "baseline_observed": False,
            "candidate_observed": True,
            "delta_known": False,
        }
    )
    assert not row.baseline_observed and row.candidate_observed and not row.delta_known
