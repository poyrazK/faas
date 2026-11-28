import pytest

from faas_sdk.preview.models import ObsRuntimeBuilds, ObsRuntimeCommandCronHealth


CLASSES = ("due", "overlap_due", "replacement_wait", "deployment_missing", "exclusive_due",
           "disabled", "suspended", "account_held", "not_due", "unknown")


@pytest.mark.parametrize("state", ("empty", "sweep_running", "never_started", "never_succeeded", "collection_failed", "partial", *CLASSES))
def test_command_cron_roundtrip_preserves_ownership_intent_sweep_false_zero_and_ages(state):
    observed = "2026-11-16T08:00:00+00:00"
    counts = dict.fromkeys(("expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable", "unexpected",
                           "nonempty_scopes", "empty_scopes", "unknown_age_scopes", "unsuccessful_collections", "enabled_scopes", "disabled_scopes",
                           "due_scopes", "inactive_scopes", "sweep_running_scopes", "sweep_idle_scopes", "sweep_never_started_scopes", "sweep_never_succeeded_scopes"), 0)
    counts.update(expected=1, observed=1, complete=1, empty_scopes=1, enabled_scopes=1, sweep_idle_scopes=1)
    classes = [{"class": cls, "nonempty_scopes": 0, "empty_scopes": 1, "unknown_age_scopes": 0} for cls in CLASSES]
    item = {"job": "schedd", "state": "available", "target_id": "a"*64, "scrape_observed_at": observed,
            "collection": {"state": "available", "observed_at": observed, "values": {"success": True, "last_success_at": observed}},
            "observation": {"state": "available", "observed_at": observed, "values": {"enabled": True}},
            "sweep": {"state": "available", "observed_at": observed, "values": {"running": False,
                       "last_started_at": "2026-11-16T07:59:40+00:00", "last_success_at": "2026-11-16T07:59:50+00:00"}},
            "classes": [{"class": cls, "state": "empty", "observed_at": observed, "values": {"schedules": 0, "unknown_age": 0}} for cls in CLASSES]}
    h = {"scope": "scheduler_ownership_scopes", "state": "empty", "counts": counts,
         "classes": classes, "incomplete": False, "items": [item], "omitted_items": 0}
    if state in CLASSES or state == "partial":
        index = CLASSES.index(state) if state in CLASSES else 0
        h.update(state="nonempty", reason="command_cron_evidence_observed")
        counts.update(nonempty_scopes=1, empty_scopes=0)
        classes[index].update(nonempty_scopes=1, empty_scopes=0)
        values = {"schedules": (1 << 53)-1, "unknown_age": 0,
                  "oldest_due_at": "2026-11-16T07:00:00+00:00", "oldest_known_due_age_seconds": 3600}
        if index < 5:
            counts["due_scopes"] = 1
        else:
            values = {"schedules": (1 << 53)-1, "unknown_age": (1 << 53)-1}
            counts["unknown_age_scopes"] = classes[index]["unknown_age_scopes"] = 1
            if index < 9:
                counts["inactive_scopes"] = 1
        item["classes"][index].update(state="nonempty", values=values)
        if state == "partial":
            h["incomplete"], item["state"] = True, "missing"
            counts.update(complete=0, missing=1)
            item["classes"][9] = {"class": "unknown", "state": "missing", "reason": "metric_missing"}
            classes[9]["empty_scopes"] = 0
    if state == "sweep_running":
        item["sweep"]["values"].update(running=True, last_started_at=observed)
        counts.update(sweep_running_scopes=1, sweep_idle_scopes=0)
    if state in ("never_started", "never_succeeded"):
        item["sweep"]["values"].pop("last_success_at")
        counts["sweep_never_succeeded_scopes"] = 1
        if state == "never_started":
            item["sweep"]["values"].pop("last_started_at")
            counts["sweep_never_started_scopes"] = 1
    if state == "collection_failed":
        h.update(state="incomplete", reason="incomplete_command_cron_evidence", incomplete=True)
        counts.update(complete=0, empty_scopes=0, unavailable=1, unsuccessful_collections=1)
        item["state"], item["collection"]["values"]["success"] = "unavailable", False
        item["classes"] = [{"class": cls, "state": "unavailable", "reason": "collection_failed"} for cls in CLASSES]
        for cls in classes:
            cls["empty_scopes"] = 0
    assert ObsRuntimeCommandCronHealth.from_dict(h).to_dict() == h
    payload = {"generated_at": observed, "scope": "selected_critical_daemon_builds", "expected_role": "control-plane-v2",
               "requested_release": "b"*40, "evidence_after": "2026-11-16T07:59:00+00:00", "state": "unknown",
               "reason": "identity_unavailable", "max_age_seconds": 120,
               "counts": dict.fromkeys(("expected", "observed", "matching", "mismatched", "missing", "stale", "unavailable", "unexpected", "distinct_revisions"), 0),
               "jobs": [], "items": [], "omitted_items": 0, "limitations": [], "command_cron_health": h}
    parsed = ObsRuntimeBuilds.from_dict(payload)
    assert isinstance(parsed.command_cron_health, ObsRuntimeCommandCronHealth)
    assert parsed.to_dict() == payload
