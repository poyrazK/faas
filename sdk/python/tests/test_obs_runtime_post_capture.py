import pytest
from faas_sdk.preview.models import ObsRuntimeBuilds, ObsRuntimePostCaptureHealth

CLASSES = (
    "publication_pending",
    "activation_pending",
    "dependency_held",
    "compatibility_held",
    "hosting_verifying",
    "hosting_retry",
    "gate_overdue",
    "unknown",
)


@pytest.mark.parametrize("state", ("empty", "known_age", "unknown_ages", "disabled", "collection_failed", *CLASSES))
def test_post_capture_roundtrip_keeps_shared_view_scope_false_zero_and_unknown_age(state):
    observed = "2026-11-15T16:00:00+00:00"
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
            "unknown_age_views",
            "unsuccessful_collections",
            "enabled_views",
            "disabled_views",
        ),
        0,
    )
    counts.update(expected=1, observed=1, complete=1, empty_views=1, enabled_views=1)
    classes = [{"class": c, "nonempty_views": 0, "empty_views": 1, "unknown_age_views": 0} for c in CLASSES]
    item = {
        "job": "imaged",
        "state": "available",
        "target_id": "a" * 64,
        "scrape_observed_at": observed,
        "collection": {
            "state": "available",
            "observed_at": observed,
            "values": {"success": True, "last_success_at": observed},
        },
        "observation": {"state": "available", "observed_at": observed, "values": {"enabled": True}},
        "classes": [
            {"class": c, "state": "empty", "observed_at": observed, "values": {"pending": 0, "unknown_age": 0}}
            for c in CLASSES
        ],
    }
    h = {
        "scope": "replica_observations_of_shared_fleet",
        "state": "empty",
        "counts": counts,
        "classes": classes,
        "incomplete": False,
        "items": [item],
        "omitted_items": 0,
    }
    if state not in ("empty", "collection_failed"):
        index = CLASSES.index(state) if state in CLASSES else 0
        h.update(state="nonempty", reason="post_capture_evidence_observed")
        counts.update(nonempty_views=1, empty_views=0)
        classes[index].update(nonempty_views=1, empty_views=0)
        values = {
            "pending": (1 << 53) - 1,
            "unknown_age": 0,
            "oldest_boundary_at": "2026-11-15T15:00:00+00:00",
            "oldest_known_age_seconds": 3600,
        }
        if state in ("unknown_ages", "unknown"):
            values = {"pending": (1 << 53) - 1, "unknown_age": (1 << 53) - 1}
            counts["unknown_age_views"] = classes[index]["unknown_age_views"] = 1
        item["classes"][index].update(state="nonempty", values=values)
        if state == "disabled":
            item["observation"]["values"]["enabled"] = False
            counts.update(enabled_views=0, disabled_views=1)
    if state == "collection_failed":
        h.update(state="incomplete", reason="incomplete_post_capture_evidence", incomplete=True)
        counts.update(complete=0, empty_views=0, unavailable=1, unsuccessful_collections=1)
        item["state"], item["collection"]["values"]["success"] = "unavailable", False
        item["classes"] = [{"class": c, "state": "unavailable", "reason": "collection_failed"} for c in CLASSES]
        for c in classes:
            c["empty_views"] = 0
    assert ObsRuntimePostCaptureHealth.from_dict(h).to_dict() == h
    payload = {
        "generated_at": observed,
        "scope": "selected_critical_daemon_builds",
        "expected_role": "control-plane-v2",
        "requested_release": "b" * 40,
        "evidence_after": "2026-11-15T15:59:00+00:00",
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
        "post_capture_health": h,
    }
    parsed = ObsRuntimeBuilds.from_dict(payload)
    assert isinstance(parsed.post_capture_health, ObsRuntimePostCaptureHealth)
    assert parsed.to_dict() == payload
