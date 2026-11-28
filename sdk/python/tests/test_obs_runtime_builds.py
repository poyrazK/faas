import pytest

from faas_sdk.preview.models import ObsRuntimeBuilds


@pytest.mark.parametrize("state", ["empty", "waiting", "collection_failed", "stopping"])
def test_handoff_evidence_roundtrip_preserves_false_zero_and_large_counts(state):
    from faas_sdk.preview.models import ObsRuntimeHandoffHealth

    observed = "2026-11-15T12:00:00+00:00"
    counts = dict.fromkeys(("expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable", "unexpected",
                           "waiting_scopes", "empty_scopes", "unsuccessful_collections", "working_recovery_scopes", "waiting_recovery_scopes", "stopping_recovery_scopes", "not_started_recovery_scopes"), 0)
    counts.update(expected=1, observed=1, complete=1, empty_scopes=1, waiting_recovery_scopes=1)
    item = {"state": "available", "target_id": "a"*64, "scrape_observed_at": observed,
            "collection": {"state": "available", "observed_at": observed, "values": {"success": True, "last_success_at": observed}},
            "backlog": {"state": "empty", "observed_at": observed, "values": {"pending": 0, "oldest_wait_seconds": 0}},
            "recovery": {"state": "waiting", "observed_at": observed, "values": {"enabled": True, "inflight": False,
                         "last_attempt_at": "2026-11-15T11:59:55+00:00", "last_completed_at": "2026-11-15T11:59:56+00:00"}}}
    h = {"state": "empty", "counts": counts, "incomplete": False, "items": [item], "omitted_items": 0}
    if state == "waiting":
        h.update(state="waiting", reason="pending_handoffs_observed")
        counts.update(waiting_scopes=1, empty_scopes=0)
        item["backlog"].update(state="waiting", values={"pending": (1 << 53)-1, "oldest_finished_at": "2026-11-15T11:00:00+00:00", "oldest_wait_seconds": 3600})
    elif state == "collection_failed":
        h.update(state="incomplete", reason="incomplete_handoff_evidence", incomplete=True)
        counts.update(complete=0, empty_scopes=0, unavailable=1, unsuccessful_collections=1)
        item["state"], item["collection"]["values"]["success"] = "unavailable", False
        item["backlog"] = {"state": "unavailable", "reason": "collection_failed"}
    elif state == "stopping":
        item["recovery"]["state"], item["recovery"]["values"]["enabled"] = "stopping", False
        counts.update(waiting_recovery_scopes=0, stopping_recovery_scopes=1)
    assert ObsRuntimeHandoffHealth.from_dict(h).to_dict() == h
    payload = {"generated_at": observed, "scope": "selected_critical_daemon_builds", "expected_role": "single-box-v2",
               "requested_release": "b"*40, "evidence_after": "2026-11-15T11:59:00+00:00", "state": "unknown",
               "reason": "identity_unavailable", "max_age_seconds": 120,
               "counts": dict.fromkeys(("expected", "observed", "matching", "mismatched", "missing", "stale", "unavailable", "unexpected", "distinct_revisions"), 0),
               "jobs": [], "items": [], "omitted_items": 0, "limitations": [], "handoff_health": h}
    response = ObsRuntimeBuilds.from_dict(payload)
    assert isinstance(response.handoff_health, ObsRuntimeHandoffHealth)
    assert response.to_dict() == payload


def test_builder_progress_keeps_false_zero_and_partial_evidence():
    from faas_sdk.preview.models import ObsRuntimeBuilderHealth

    observed = "2026-11-15T12:00:00+00:00"
    counts = dict.fromkeys(("expected", "observed", "complete", "missing", "stale", "conflicting", "unavailable",
                           "unexpected", "overdue_workers", "waiting_workers", "working_workers", "draining_workers",
                           "waiting_queues", "saturated_slots"), 0)
    counts.update(expected=1, observed=1, complete=1, draining_workers=1)
    worker = {"state": "draining", "observed_at": observed, "values": {
        "enabled": False, "inflight": False, "started_at": "2026-11-15T11:00:00+00:00",
        "last_attempt_at": "2026-11-15T11:59:55+00:00", "last_completed_at": "2026-11-15T11:59:56+00:00",
        "interval_seconds": 2, "busy_allowance_seconds": 900, "progress_age_seconds": 4, "progress_allowance_seconds": 64}}
    item = {"state": "available", "target_id": "b"*64, "scrape_observed_at": observed, "worker": worker,
            "slots": {"state": "available", "observed_at": observed, "values": {"used": 0, "limit": 1}},
            "queues": [{"state": "available", "observed_at": observed, "queue": name,
                        "values": {"enabled": False, "waiting": 0, "active": 0, "capacity": 0, "workers": 2, "oldest_wait_seconds": 0}}
                       for name in ("build", "cancel")]}
    payload = {"state": "observed", "counts": counts, "incomplete": False, "items": [item], "omitted_items": 0}
    assert ObsRuntimeBuilderHealth.from_dict(payload).to_dict() == payload
    counts.update(complete=0, missing=1, draining_workers=0, overdue_workers=1)
    payload.update(state="overdue", reason="worker_progress_overdue", incomplete=True)
    worker["state"] = "overdue"
    worker["values"].update(enabled=True, progress_age_seconds=90, last_attempt_at="2026-11-15T11:58:29+00:00", last_completed_at="2026-11-15T11:58:30+00:00")
    item.update(state="missing", slots={"state": "missing", "reason": "metric_missing"})
    assert ObsRuntimeBuilderHealth.from_dict(payload).to_dict() == payload


def test_build_evidence_keeps_full_counts_and_missing_identity_absent():
    payload = {
        "generated_at": "2026-11-15T12:00:00+00:00",
        "scope": "selected_critical_daemon_builds",
        "expected_role": "control-plane-v1",
        "requested_release": "a" * 40,
        "evidence_after": "2026-11-15T11:59:00+00:00",
        "state": "mixed",
        "reason": "multiple_observed_revisions",
        "max_age_seconds": 120,
        "counts": {
            "expected": 123, "observed": 123, "matching": 122,
            "mismatched": 1, "missing": 1, "stale": 0,
            "unavailable": 0, "unexpected": 1, "distinct_revisions": 2,
        },
        "jobs": [{
            "job": "vmmd", "daemon": "vmmd",
            "inventory_source": "active_configured_compute_registry",
            "state": "available", "expected": 40,
        }],
        "items": [{
            "job": "vmmd", "daemon": "vmmd", "node": "new-node",
            "state": "missing", "reason": "expected_target_missing",
        }],
        "omitted_items": 123,
        "limitations": ["advisory_not_deployment_qualification"],
    }
    response = ObsRuntimeBuilds.from_dict(payload)
    assert response.counts.distinct_revisions == 2
    assert response.to_dict() == payload
    assert "git_sha" not in response.items[0].to_dict()


def test_unselected_role_and_empty_preview_roundtrip():
    payload = {
        "generated_at": "2026-11-15T12:00:00+00:00",
        "scope": "selected_critical_daemon_builds",
        "expected_role": "",
        "requested_release": "a" * 40,
        "evidence_after": "2026-11-15T11:59:00+00:00",
        "state": "unknown", "reason": "expected_role_not_selected",
        "max_age_seconds": 120,
        "counts": dict.fromkeys((
            "expected", "observed", "matching", "mismatched", "missing",
            "stale", "unavailable", "unexpected", "distinct_revisions",
        ), 0),
        "jobs": [], "items": [], "omitted_items": 0, "limitations": [],
    }
    assert ObsRuntimeBuilds.from_dict(payload).to_dict() == payload


@pytest.mark.parametrize("role,jobs", [
    ("single-box-v2", ["apid", "gatewayd", "schedd", "vmmd", "builderd", "imaged"]),
    ("control-plane-v2", ["apid", "gatewayd", "schedd", "gatewayd-internal", "schedd-compute", "vmmd", "builderd", "imaged"]),
])
def test_v2_build_profiles_preserve_extended_job_coverage(role, jobs):
    payload = {
        "generated_at": "2026-11-15T12:00:00+00:00", "scope": "selected_critical_daemon_builds",
        "expected_role": role, "requested_release": "a" * 40,
        "evidence_after": "2026-11-15T11:59:00+00:00", "state": "matching", "max_age_seconds": 120,
        "counts": {"expected": len(jobs), "observed": len(jobs), "matching": len(jobs), "mismatched": 0,
                   "missing": 0, "stale": 0, "unavailable": 0, "unexpected": 0, "distinct_revisions": 1},
        "jobs": [{"job": job, "daemon": {"gatewayd": "gatewayd-public", "schedd-compute": "schedd"}.get(job, job),
                  "inventory_source": "active_configured_compute_registry" if role == "control-plane-v2" and i >= 3 else "scrape_targets_minimum",
                  "state": "available", "expected": 1} for i, job in enumerate(jobs)],
        "items": [], "omitted_items": len(jobs), "limitations": [],
    }
    response = ObsRuntimeBuilds.from_dict(payload)
    assert [job.job for job in response.jobs[-2:]] == ["builderd", "imaged"]
    assert response.to_dict() == payload


def test_sampled_process_continuity_roundtrip():
    from faas_sdk.preview.models import ObsRuntimeProcess, ObsRuntimeProcessItem
    names = ("fresh", "missing", "stale", "conflicting", "unavailable", "compared", "changed", "unchanged",
             "same_revision_changes", "different_revision_changes", "unknown_revision_changes", "new_targets",
             "removed_targets", "repeated_samples", "comparison_unavailable")
    counts = dict.fromkeys(names, 0)
    counts.update(fresh=4, compared=4, changed=1, unchanged=3, same_revision_changes=1)
    payload = {"state": "changed", "reason": "observed_process_change", "comparison_at": "2026-11-15T11:59:30+00:00",
               "counts": counts, "incomplete": False}
    parsed = ObsRuntimeProcess.from_dict(payload)
    assert parsed.counts.same_revision_changes == 1
    assert parsed.to_dict() == payload
    item = {"state": "fresh", "uptime_seconds": 5, "observed_at": "2026-11-15T12:00:00+00:00",
            "comparison": "changed", "previous_uptime_seconds": 100,
            "previous_observed_at": "2026-11-15T11:59:30+00:00", "revision_relation": "same"}
    assert ObsRuntimeProcessItem.from_dict(item).to_dict() == item


def test_loop_health_keeps_false_and_partial_stall_reports():
    from faas_sdk.preview.models import ObsRuntimeLoopHealth
    counts = {"expected": 6, "observed": 6, "no_reported_stall": 5, "stalled": 0,
              "missing": 1, "stale": 0, "conflicting": 0, "unavailable": 0,
              "reported_stalls": 1, "unexpected": 0}
    payload = {"state": "stalled", "reason": "reported_loop_stall", "counts": counts,
               "incomplete": True, "omitted_items": 4, "items": [
                   {"job": "schedd", "daemon": "schedd", "loop": "main", "state": "missing",
                    "reason": "loop_age_missing", "reported_stalled": True},
                   {"job": "apid", "daemon": "apid", "loop": "runtime", "state": "no_reported_stall",
                    "reported_stalled": False, "age_seconds": 0},
               ]}
    parsed = ObsRuntimeLoopHealth.from_dict(payload)
    assert parsed.items[0].reported_stalled is True
    assert parsed.items[1].reported_stalled is False
    assert parsed.to_dict() == payload
