import pytest

from faas_sdk.preview.models import ObsRuntimeCommandCronOutcomes, ObsRuntimeBuilds

GROUPS = (
    ("sweep", ("success", "error", "canceled")),
    ("decision", ("disabled", "not_due", "account_held", "plan_limit", "replacement_wait", "not_owned", "invalid_schedule", "lookup_error", "store_unavailable", "no_live_deployment", "canceled", "legacy_queued", "legacy_skipped", "legacy_stale", "legacy_error")),
    ("admission", ("queued", "exclusive_pending", "coalesced", "skipped_overlap", "missed_deadline", "stale", "deployment_unavailable", "rejected", "error", "canceled", "unknown")),
)
DIMENSIONS = tuple({"source": source, "outcome": outcome} for source, outcomes in GROUPS for outcome in outcomes)


@pytest.mark.parametrize("state", ("baseline", "unchanged", "counter_reset", "sparse", *(d["source"]+"/"+d["outcome"] for d in DIMENSIONS)))
def test_cron_outcomes_roundtrip_preserves_each_source_zero_and_uncertainty(state):
    now, at = "2026-11-16T12:00:30+00:00", "2026-11-16T12:00:00+00:00"
    counts = dict.fromkeys(("expected", "observed", "increased", "unchanged", "baseline", "missing", "stale", "conflicting", "unavailable", "unexpected"), 0)
    counts.update(expected=29, observed=29)
    outcomes = [{**d, **dict.fromkeys(("increased", "unchanged", "baseline", "unknown"), 0)} for d in DIMENSIONS]
    process = {"state": "fresh", "uptime_seconds": 130, "observed_at": now, "comparison": "baseline"}
    h = {"scope": "process_local_counter_observations", "state": "baseline", "reason": "comparison_not_requested", "counts": counts, "outcomes": outcomes, "incomplete": False,
         "items": [{"job": "schedd", "target_id": "a"*64, "scrape_observed_at": now, "process": process, "signals": []}], "omitted_items": 0}
    if state != "baseline":
        process.update(comparison="unchanged", previous_uptime_seconds=100, previous_observed_at=at)
        h["comparison_at"], h["state"] = at, "unchanged"
        h.pop("reason")
    for i, d in enumerate(DIMENSIONS):
        s = {**d, "state": "baseline", "current": {"value": (1 << 53)-1, "observed_at": now}}
        category = "baseline" if state == "baseline" else "unchanged"
        if state != "baseline":
            s.update(state="unchanged", previous={"value": (1 << 53)-1, "observed_at": at}, delta=0)
        if state == "sparse" and i == 0:
            s = {**d, "state": "missing", "reason": "metric_missing"}
            counts["observed"] -= 1
            category = "missing"
            h.update(state="incomplete", reason="incomplete_cron_outcome_evidence", incomplete=True)
        if d["source"]+"/"+d["outcome"] == state:
            s["previous"]["value"] -= 3
            s.update(state="increased", delta=3)
            category = "increased"
            h.update(state="increased", reason="cron_outcome_increases_observed")
        if state == "counter_reset" and i == 26:
            s["current"]["value"] = 1
            s.pop("delta")
            s.update(state="unavailable", reason="counter_reset")
            category = "unavailable"
            h.update(state="incomplete", reason="incomplete_cron_outcome_evidence", incomplete=True)
        counts[category] += 1
        outcomes[i][category if category in ("increased", "unchanged", "baseline") else "unknown"] += 1
        h["items"][0]["signals"].append(s)
    assert ObsRuntimeCommandCronOutcomes.from_dict(h).to_dict() == h
    payload = {"generated_at": now, "scope": "selected_critical_daemon_builds", "expected_role": "control-plane-v2", "requested_release": "b"*40,
               "evidence_after": "2026-11-16T11:59:00+00:00", "state": "unknown", "reason": "identity_unavailable", "max_age_seconds": 120,
               "counts": dict.fromkeys(("expected", "observed", "matching", "mismatched", "missing", "stale", "unavailable", "unexpected", "distinct_revisions"), 0),
               "jobs": [], "items": [], "omitted_items": 0, "limitations": [], "command_cron_outcomes": h}
    parsed = ObsRuntimeBuilds.from_dict(payload)
    assert isinstance(parsed.command_cron_outcomes, ObsRuntimeCommandCronOutcomes)
    assert parsed.to_dict() == payload
