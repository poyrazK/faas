import pytest
from faas_sdk.preview.models import ObsRuntimeAppTaskResults, ObsRuntimeBuilds


@pytest.mark.parametrize(
    "state",
    (
        "baseline",
        "unchanged",
        "succeeded",
        "failed",
        "timed_out",
        "cancelled",
        "queued",
        "requeued_restores",
        "failed_runs",
        "recovery_cancelled",
        "counter_reset",
        "sparse",
    ),
)
def test_results_roundtrip_preserves_returned_status_recovery_zero_and_uncertainty(state):
    now, at = "2026-11-16T10:00:30+00:00", "2026-11-16T10:00:00+00:00"
    dimensions = [
        {"source": "completion", "kind": k, "outcome": s}
        for k in ("release", "manual", "cron")
        for s in ("succeeded", "failed", "timed_out", "cancelled", "queued")
    ]
    dimensions += [{"source": "recovery", "outcome": s} for s in ("requeued_restores", "failed_runs", "cancelled")]
    counts = dict.fromkeys(
        (
            "expected",
            "observed",
            "increased",
            "unchanged",
            "baseline",
            "missing",
            "stale",
            "conflicting",
            "unavailable",
            "unexpected",
        ),
        0,
    )
    counts.update(expected=18, observed=18)
    outcomes = [{**d, **dict.fromkeys(("increased", "unchanged", "baseline", "unknown"), 0)} for d in dimensions]
    process = {"state": "fresh", "uptime_seconds": 130, "observed_at": now, "comparison": "baseline"}
    h = {
        "scope": "process_local_counter_observations",
        "state": "baseline",
        "reason": "comparison_not_requested",
        "counts": counts,
        "outcomes": outcomes,
        "incomplete": False,
        "items": [
            {"job": "schedd", "target_id": "a" * 64, "scrape_observed_at": now, "process": process, "signals": []}
        ],
        "omitted_items": 0,
    }
    if state != "baseline":
        process.update(comparison="unchanged", previous_uptime_seconds=100, previous_observed_at=at)
        h["comparison_at"], h["state"] = at, "unchanged"
        h.pop("reason")
    for i, d in enumerate(dimensions):
        s = {**d, "state": "baseline", "current": {"value": (1 << 53) - 1, "observed_at": now}}
        category = "baseline" if state == "baseline" else "unchanged"
        if state != "baseline":
            s.update(state="unchanged", previous={"value": (1 << 53) - 1, "observed_at": at}, delta=0)
        if state == "sparse" and i == 15:
            s = {**d, "state": "missing", "reason": "metric_missing"}
            counts["observed"] -= 1
            category = "missing"
            h.update(state="incomplete", reason="incomplete_result_evidence", incomplete=True)
        selected_source = (
            "recovery" if state in ("requeued_restores", "failed_runs", "recovery_cancelled") else "completion"
        )
        selected_outcome = "cancelled" if state == "recovery_cancelled" else state
        if d["source"] == selected_source and d["outcome"] == selected_outcome:
            s["previous"]["value"] -= 3
            s.update(state="increased", delta=3)
            category = "increased"
            h.update(state="increased", reason="result_increases_observed")
        if state == "counter_reset" and i == 1:
            s["current"]["value"] = 1
            s.pop("delta")
            s.update(state="unavailable", reason="counter_reset")
            category = "unavailable"
            h.update(state="incomplete", reason="incomplete_result_evidence", incomplete=True)
        counts[category] += 1
        outcomes[i][category if category in ("increased", "unchanged", "baseline") else "unknown"] += 1
        h["items"][0]["signals"].append(s)
    assert ObsRuntimeAppTaskResults.from_dict(h).to_dict() == h
    payload = {
        "generated_at": now,
        "scope": "selected_critical_daemon_builds",
        "expected_role": "control-plane-v2",
        "requested_release": "b" * 40,
        "evidence_after": "2026-11-16T09:59:00+00:00",
        "state": "unknown",
        "reason": "identity_unavailable",
        "max_age_seconds": 120,
        "counts": dict.fromkeys(
            (
                "expected",
                "observed",
                "matching",
                "mismatched",
                "missing",
                "stale",
                "unavailable",
                "unexpected",
                "distinct_revisions",
            ),
            0,
        ),
        "jobs": [],
        "items": [],
        "omitted_items": 0,
        "limitations": [],
        "app_task_results": h,
    }
    parsed = ObsRuntimeBuilds.from_dict(payload)
    assert isinstance(parsed.app_task_results, ObsRuntimeAppTaskResults)
    assert parsed.to_dict() == payload
