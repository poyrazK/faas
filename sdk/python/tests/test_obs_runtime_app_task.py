import pytest

from faas_sdk.preview.models import ObsRuntimeAppTaskHealth, ObsRuntimeBuilds


CLASSES = tuple((kind, cls) for kind in ("release", "manual", "cron") for cls in
                ("queued", "retry_wait", "held", "deadline_overdue", "restoring", "running", "cancellation_pending", "lease_expired", "unknown"))


@pytest.mark.parametrize("state", ("empty", "disabled", "stopped", "saturated", "collection_failed", "partial", *CLASSES))
def test_app_task_roundtrip_keeps_kind_class_scope_local_slots_false_zero_and_unknown_age(state):
    observed = "2026-11-15T21:00:00+00:00"
    counts = dict.fromkeys(("expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable", "unexpected",
                           "nonempty_views", "empty_views", "unknown_age_views", "unsuccessful_collections", "enabled_views", "disabled_views",
                           "dispatch_enabled_views", "dispatch_disabled_views", "runner_running_views", "runner_stopped_views", "occupied_views", "saturated_views"), 0)
    counts.update(expected=1, observed=1, complete=1, empty_views=1, enabled_views=1, dispatch_enabled_views=1, runner_running_views=1)
    classes = [{"kind": kind, "class": cls, "nonempty_views": 0, "empty_views": 1, "unknown_age_views": 0} for kind, cls in CLASSES]
    item = {"job": "schedd", "state": "available", "target_id": "a"*64, "scrape_observed_at": observed,
            "collection": {"state": "available", "observed_at": observed, "values": {"success": True, "last_success_at": observed}},
            "observation": {"state": "available", "observed_at": observed, "values": {"enabled": True}},
            "dispatch": {"state": "running", "observed_at": observed, "values": {"enabled": True, "running": True, "slots_configured": 4, "slots_in_use": 0}},
            "classes": [{"kind": kind, "class": cls, "state": "empty", "observed_at": observed, "values": {"pending": 0, "unknown_age": 0}} for kind, cls in CLASSES]}
    h = {"scope": "replica_observations_of_shared_fleet", "state": "empty", "counts": counts,
         "classes": classes, "incomplete": False, "items": [item], "omitted_items": 0}
    if isinstance(state, tuple) or state == "partial":
        index = CLASSES.index(state) if isinstance(state, tuple) else 0
        h.update(state="nonempty", reason="app_task_evidence_observed")
        counts.update(nonempty_views=1, empty_views=0)
        classes[index].update(nonempty_views=1, empty_views=0)
        values = {"pending": (1 << 53)-1, "unknown_age": 0,
                  "oldest_boundary_at": "2026-11-15T20:00:00+00:00", "oldest_known_age_seconds": 3600}
        if isinstance(state, tuple) and state[1] in ("restoring", "retry_wait", "held", "unknown"):
            values = {"pending": (1 << 53)-1, "unknown_age": (1 << 53)-1}
            counts["unknown_age_views"] = classes[index]["unknown_age_views"] = 1
        item["classes"][index].update(state="nonempty", values=values)
        if state == "partial":
            h["incomplete"], item["state"] = True, "missing"
            counts.update(complete=0, missing=1)
            item["classes"][1] = {"kind": "release", "class": "retry_wait", "state": "missing", "reason": "metric_missing"}
            classes[1]["empty_views"] = 0
    if state in ("disabled", "stopped", "saturated"):
        d = item["dispatch"]
        if state == "disabled":
            d["state"] = "disabled"
            d["values"].update(enabled=False, running=False)
            counts.update(dispatch_enabled_views=0, runner_running_views=0, dispatch_disabled_views=1)
        elif state == "stopped":
            d["state"] = "stopped"
            d["values"]["running"] = False
            counts.update(runner_running_views=0, runner_stopped_views=1)
        else:
            d["values"]["slots_in_use"] = 4
            counts.update(occupied_views=1, saturated_views=1)
        d["values"].update(last_claim_success_at=observed, last_sweep_success_at=observed)
    if state == "collection_failed":
        h.update(state="incomplete", reason="incomplete_app_task_evidence", incomplete=True)
        counts.update(complete=0, empty_views=0, unavailable=1, unsuccessful_collections=1)
        item["state"], item["collection"]["values"]["success"] = "unavailable", False
        item["classes"] = [{"kind": kind, "class": cls, "state": "unavailable", "reason": "collection_failed"} for kind, cls in CLASSES]
        for cls in classes:
            cls["empty_views"] = 0
    assert ObsRuntimeAppTaskHealth.from_dict(h).to_dict() == h
    payload = {"generated_at": observed, "scope": "selected_critical_daemon_builds", "expected_role": "control-plane-v2",
               "requested_release": "b"*40, "evidence_after": "2026-11-15T20:59:00+00:00", "state": "unknown",
               "reason": "identity_unavailable", "max_age_seconds": 120,
               "counts": dict.fromkeys(("expected", "observed", "matching", "mismatched", "missing", "stale", "unavailable", "unexpected", "distinct_revisions"), 0),
               "jobs": [], "items": [], "omitted_items": 0, "limitations": [], "app_task_health": h}
    parsed = ObsRuntimeBuilds.from_dict(payload)
    assert isinstance(parsed.app_task_health, ObsRuntimeAppTaskHealth)
    assert parsed.to_dict() == payload
