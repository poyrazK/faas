import pytest
from faas_sdk.preview.models import ObsRuntimeBuilds, ObsRuntimePreparationHealth


@pytest.mark.parametrize("state", ["empty", "known_age", "unknown_ages", "disabled", "collection_failed"])
def test_preparation_roundtrip_preserves_unknown_ages_false_and_zero(state):
    observed = "2026-11-15T12:00:00+00:00"
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
            "waiting_scopes",
            "empty_scopes",
            "unknown_age_scopes",
            "unsuccessful_collections",
            "enabled_scopes",
            "disabled_scopes",
        ),
        0,
    )
    counts.update(expected=1, observed=1, complete=1, empty_scopes=1, enabled_scopes=1)
    phases = [
        {"phase": p, "waiting_scopes": 0, "empty_scopes": 1, "unknown_age_scopes": 0}
        for p in ("preparing", "layer_published", "scan_complete")
    ]
    item = {
        "state": "available",
        "target_id": "a" * 64,
        "scrape_observed_at": observed,
        "collection": {
            "state": "available",
            "observed_at": observed,
            "values": {"success": True, "last_success_at": observed},
        },
        "observation": {"state": "available", "observed_at": observed, "values": {"enabled": True}},
        "phases": [
            {"phase": p["phase"], "state": "empty", "observed_at": observed, "values": {"pending": 0, "unknown_age": 0}}
            for p in phases
        ],
    }
    h = {"state": "empty", "counts": counts, "phases": phases, "incomplete": False, "items": [item], "omitted_items": 0}
    if state in ("known_age", "unknown_ages", "disabled"):
        h.update(state="waiting", reason="pending_preparations_observed")
        counts.update(waiting_scopes=1, empty_scopes=0)
        phases[0].update(waiting_scopes=1, empty_scopes=0)
        item["phases"][0].update(
            state="waiting",
            values={
                "pending": (1 << 53) - 1,
                "unknown_age": 0,
                "oldest_phase_started_at": "2026-11-15T11:00:00+00:00",
                "oldest_known_age_seconds": 3600,
            },
        )
        if state == "unknown_ages":
            counts["unknown_age_scopes"] = phases[0]["unknown_age_scopes"] = 1
            item["phases"][0]["values"] = {"pending": (1 << 53) - 1, "unknown_age": (1 << 53) - 1}
        if state == "disabled":
            counts.update(enabled_scopes=0, disabled_scopes=1)
            item["observation"]["values"]["enabled"] = False
    if state == "collection_failed":
        h.update(state="incomplete", reason="incomplete_preparation_evidence", incomplete=True)
        counts.update(complete=0, empty_scopes=0, unavailable=1, unsuccessful_collections=1)
        item["state"], item["collection"]["values"]["success"] = "unavailable", False
        item["phases"] = [{"phase": p["phase"], "state": "unavailable", "reason": "collection_failed"} for p in phases]
        for p in phases:
            p["empty_scopes"] = 0
    assert ObsRuntimePreparationHealth.from_dict(h).to_dict() == h
    payload = {
        "generated_at": observed,
        "scope": "selected_critical_daemon_builds",
        "expected_role": "single-box-v2",
        "requested_release": "b" * 40,
        "evidence_after": "2026-11-15T11:59:00+00:00",
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
        "preparation_health": h,
    }
    parsed = ObsRuntimeBuilds.from_dict(payload)
    assert isinstance(parsed.preparation_health, ObsRuntimePreparationHealth)
    assert parsed.to_dict() == payload
