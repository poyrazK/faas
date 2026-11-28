import pytest
from faas_sdk.preview.models import ObsRuntimeAppTaskOperations, ObsRuntimeBuilds

OPS = ("restore", "renew", "execute", "destroy", "complete")
OUTCOMES = ("success", "error", "canceled", "lease_lost", "empty", "cancellation_pending", "capacity")


@pytest.mark.parametrize(
    "state", ("baseline", "unchanged", "error", "canceled", "lease_lost", "capacity", "counter_reset", "sparse")
)
def test_operations_roundtrip_keeps_points_delta_zero_sparse_counters_and_process_proof(state):
    now, at = "2026-11-16T09:00:30+00:00", "2026-11-16T09:00:00+00:00"
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
    counts.update(expected=35, observed=35)
    operations = [
        {
            "operation": op,
            **dict.fromkeys(
                (
                    "increased",
                    "unchanged",
                    "baseline",
                    "unknown",
                    "error_increases",
                    "cancellations",
                    "lease_losses",
                    "capacity_refusals",
                ),
                0,
            ),
        }
        for op in OPS
    ]
    process = {"state": "fresh", "uptime_seconds": 130, "observed_at": now, "comparison": "baseline"}
    h = {
        "scope": "process_local_counter_observations",
        "state": "baseline",
        "reason": "comparison_not_requested",
        "counts": counts,
        "operations": operations,
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
    for i, op in enumerate(OPS):
        for outcome in OUTCOMES:
            s = {
                "operation": op,
                "outcome": outcome,
                "state": "baseline",
                "current": {"value": (1 << 53) - 1, "observed_at": now},
            }
            category = "baseline" if state == "baseline" else "unchanged"
            if state != "baseline":
                s.update(state="unchanged", previous={"value": (1 << 53) - 1, "observed_at": at}, delta=0)
            if state == "sparse" and outcome == "capacity":
                s = {"operation": op, "outcome": outcome, "state": "missing", "reason": "metric_missing"}
                counts["observed"] -= 1
                category = "missing"
                h.update(state="incomplete", reason="incomplete_operation_evidence", incomplete=True)
            if op == "restore" and state == outcome and state in ("error", "canceled", "lease_lost", "capacity"):
                s["previous"]["value"] -= 3
                s.update(state="increased", delta=3)
                category = "increased"
                h.update(state="increased", reason="operation_increases_observed")
                operations[i][
                    {
                        "error": "error_increases",
                        "canceled": "cancellations",
                        "lease_lost": "lease_losses",
                        "capacity": "capacity_refusals",
                    }[state]
                ] = 1
            if state == "counter_reset" and op == "restore" and outcome == "error":
                s["current"]["value"] = 1
                s.pop("delta")
                s.update(state="unavailable", reason="counter_reset")
                category = "unavailable"
                h.update(state="incomplete", reason="incomplete_operation_evidence", incomplete=True)
            counts[category] += 1
            operations[i][category if category in ("increased", "unchanged", "baseline") else "unknown"] += 1
            h["items"][0]["signals"].append(s)
    assert ObsRuntimeAppTaskOperations.from_dict(h).to_dict() == h
    payload = {
        "generated_at": now,
        "scope": "selected_critical_daemon_builds",
        "expected_role": "control-plane-v2",
        "requested_release": "b" * 40,
        "evidence_after": "2026-11-16T08:59:00+00:00",
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
        "app_task_operations": h,
    }
    parsed = ObsRuntimeBuilds.from_dict(payload)
    assert isinstance(parsed.app_task_operations, ObsRuntimeAppTaskOperations)
    assert parsed.to_dict() == payload
