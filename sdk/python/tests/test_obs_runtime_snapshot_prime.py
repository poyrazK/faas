import pytest

from faas_sdk.preview.models import ObsRuntimeBuilds, ObsRuntimeSnapshotPrimeHealth


@pytest.mark.parametrize("job", ["schedd", "schedd-compute"])
@pytest.mark.parametrize("state", ["empty", "known_age", "unknown_ages", "disabled", "collection_failed", "recorded", "unknown_class"])
def test_snapshot_prime_roundtrip_preserves_unknown_ages_false_and_zero(state, job):
    observed = "2026-11-15T12:00:00+00:00"
    counts = dict.fromkeys(("expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable", "unexpected",
                           "nonempty_scopes", "empty_scopes", "unknown_age_scopes", "unsuccessful_collections", "enabled_scopes", "disabled_scopes"), 0)
    counts.update(expected=1, observed=1, complete=1, empty_scopes=1, enabled_scopes=1)
    classes = [{"class": p, "nonempty_scopes": 0, "empty_scopes": 1, "unknown_age_scopes": 0} for p in ("waiting", "release_blocked", "active", "publication_pending", "snapshot_recorded", "unknown")]
    item = {"job": job, "state": "available", "target_id": "a"*64, "scrape_observed_at": observed,
            "collection": {"state": "available", "observed_at": observed, "values": {"success": True, "last_success_at": observed}},
            "observation": {"state": "available", "observed_at": observed, "values": {"enabled": True}},
            "classes": [{"class": p["class"], "state": "empty", "observed_at": observed, "values": {"pending": 0, "unknown_age": 0}} for p in classes]}
    h = {"state": "empty", "counts": counts, "classes": classes, "incomplete": False, "items": [item], "omitted_items": 0}
    if state in ("known_age", "unknown_ages", "disabled", "recorded", "unknown_class"):
        h.update(state="nonempty", reason="snapshot_attempts_observed")
        counts.update(nonempty_scopes=1, empty_scopes=0)
        classes[0].update(nonempty_scopes=1, empty_scopes=0)
        item["classes"][0].update(state="nonempty", values={"pending": (1 << 53)-1, "unknown_age": 0,
                               "oldest_attempt_at": "2026-11-15T11:00:00+00:00", "oldest_known_age_seconds": 3600})
        if state in ("unknown_ages", "unknown_class"):
            counts["unknown_age_scopes"] = classes[0]["unknown_age_scopes"] = 1
            item["classes"][0]["values"] = {"pending": (1 << 53)-1, "unknown_age": (1 << 53)-1}
        if state == "disabled":
            counts.update(enabled_scopes=0, disabled_scopes=1)
            item["observation"]["values"]["enabled"] = False
    if state in ("recorded", "unknown_class"):
        index = 4 if state == "recorded" else 5
        item["classes"][index].update(state=item["classes"][0]["state"], values=item["classes"][0]["values"])
        item["classes"][0].update(state="empty", values={"pending": 0, "unknown_age": 0})
        for k in ("nonempty_scopes", "empty_scopes", "unknown_age_scopes"):
            classes[index][k], classes[0][k] = classes[0][k], classes[index][k]
    if state == "collection_failed":
        h.update(state="incomplete", reason="incomplete_snapshot_prime_evidence", incomplete=True)
        counts.update(complete=0, empty_scopes=0, unavailable=1, unsuccessful_collections=1)
        item["state"], item["collection"]["values"]["success"] = "unavailable", False
        item["classes"] = [{"class": p["class"], "state": "unavailable", "reason": "collection_failed"} for p in classes]
        for p in classes:
            p["empty_scopes"] = 0
    assert ObsRuntimeSnapshotPrimeHealth.from_dict(h).to_dict() == h
    payload = {"generated_at": observed, "scope": "selected_critical_daemon_builds", "expected_role": "control-plane-v2" if job == "schedd-compute" else "single-box-v2",
               "requested_release": "b"*40, "evidence_after": "2026-11-15T11:59:00+00:00", "state": "unknown",
               "reason": "identity_unavailable", "max_age_seconds": 120,
               "counts": dict.fromkeys(("expected", "observed", "matching", "mismatched", "missing", "stale", "unavailable", "unexpected", "distinct_revisions"), 0),
               "jobs": [], "items": [], "omitted_items": 0, "limitations": [], "snapshot_prime_health": h}
    parsed = ObsRuntimeBuilds.from_dict(payload)
    assert isinstance(parsed.snapshot_prime_health, ObsRuntimeSnapshotPrimeHealth)
    assert parsed.to_dict() == payload
