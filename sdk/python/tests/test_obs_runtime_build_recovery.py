import pytest
from faas_sdk.preview.models import ObsRuntimeBuildRecoveryHealth, ObsRuntimeBuilds


@pytest.mark.parametrize("state", ("empty", "running", "cleanup", "both", "collection_failed", "partial", "maximum"))
def test_build_recovery_roundtrip_retains_scope_components_false_zero_and_exact_counts(state):
    observed = "2026-11-15T20:00:00+00:00"
    counts = dict.fromkeys(
        (
            "expected",
            "observed",
            "complete",
            "missing",
            "stale",
            "conflicting",
            "unavailable",
            "unexpected",
            "nonempty_views",
            "empty_views",
            "running_views",
            "cleanup_views",
            "retried_cleanup_views",
            "unsuccessful_collections",
        ),
        0,
    )
    counts.update(expected=1, observed=1, complete=1, empty_views=1)
    item = {
        "job": "apid",
        "state": "available",
        "target_id": "a" * 64,
        "scrape_observed_at": observed,
        "collection": {
            "state": "available",
            "observed_at": observed,
            "values": {"success": True, "last_success_at": observed},
        },
        "running": {"state": "empty", "observed_at": observed, "values": {"count": 0}},
        "cleanup": {"state": "empty", "observed_at": observed, "values": {"pending": 0, "retried": 0}},
    }
    h = {
        "scope": "replica_observations_of_shared_fleet",
        "state": "empty",
        "counts": counts,
        "incomplete": False,
        "items": [item],
        "omitted_items": 0,
    }
    if state not in ("empty", "collection_failed"):
        h.update(state="nonempty", reason="build_recovery_evidence_observed")
        counts.update(nonempty_views=1, empty_views=0)
        if state in ("running", "both", "maximum"):
            counts["running_views"] = 1
            item["running"].update(
                state="nonempty",
                values={
                    "count": (1 << 53) - 1,
                    "oldest_started_at": "2026-11-15T19:00:00+00:00",
                    "oldest_age_seconds": 3600,
                },
            )
        if state in ("cleanup", "both", "partial", "maximum"):
            counts.update(cleanup_views=1, retried_cleanup_views=1)
            item["cleanup"].update(
                state="nonempty",
                values={
                    "pending": (1 << 53) - 1,
                    "retried": (1 << 53) - 1,
                    "oldest_created_at": "2026-11-14T20:00:00+00:00",
                    "oldest_age_seconds": 86400,
                },
            )
        if state == "partial":
            h["incomplete"], item["state"] = True, "missing"
            counts.update(complete=0, missing=1)
            item["running"] = {"state": "missing", "reason": "metric_missing"}
    if state == "collection_failed":
        h.update(state="incomplete", reason="incomplete_build_recovery_evidence", incomplete=True)
        counts.update(complete=0, empty_views=0, unavailable=1, unsuccessful_collections=1)
        item["state"], item["collection"]["values"]["success"] = "unavailable", False
        for name in ("running", "cleanup"):
            item[name] = {"state": "unavailable", "reason": "collection_failed"}
    assert ObsRuntimeBuildRecoveryHealth.from_dict(h).to_dict() == h
    payload = {
        "generated_at": observed,
        "scope": "selected_critical_daemon_builds",
        "expected_role": "control-plane-v2",
        "requested_release": "b" * 40,
        "evidence_after": "2026-11-15T19:59:00+00:00",
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
        "build_recovery_health": h,
    }
    parsed = ObsRuntimeBuilds.from_dict(payload)
    assert isinstance(parsed.build_recovery_health, ObsRuntimeBuildRecoveryHealth)
    assert parsed.to_dict() == payload
