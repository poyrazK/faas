import pytest
from faas_sdk.preview.models import ObsRuntimeBuilds, ObsRuntimeCommandCronTiming

NAMES = (
    "le_1_seconds",
    "le_5_seconds",
    "le_15_seconds",
    "le_30_seconds",
    "le_60_seconds",
    "le_120_seconds",
    "le_300_seconds",
    "le_600_seconds",
    "le_1800_seconds",
    "le_3600_seconds",
    "le_inf",
    "timed",
    "unknown_timestamps",
)


@pytest.mark.parametrize(
    "state", ("baseline", "unchanged", "unknown_timestamps", "sparse", "counter_reset", *NAMES[:11])
)
def test_cron_timing_roundtrip_preserves_buckets_zero_and_uncertainty(state):
    now, at = "2026-11-16T13:00:30+00:00", "2026-11-16T13:00:00+00:00"
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
    counts.update(expected=13, observed=13)
    summaries = [
        {"signal": name, **dict.fromkeys(("increased", "unchanged", "baseline", "unknown"), 0)} for name in NAMES
    ]
    process = {"state": "fresh", "uptime_seconds": 130, "observed_at": now, "comparison": "baseline"}
    h = {
        "scope": "process_local_counter_observations",
        "state": "baseline",
        "reason": "comparison_not_requested",
        "counts": counts,
        "signals": summaries,
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
    for i, name in enumerate(NAMES):
        s = {"signal": name, "state": "baseline", "current": {"value": (1 << 53) - 1, "observed_at": now}}
        category = "baseline" if state == "baseline" else "unchanged"
        if state != "baseline":
            s.update(state="unchanged", previous={"value": (1 << 53) - 1, "observed_at": at}, delta=0)
        if state in NAMES[:11] and i < 12:
            # Endpoints and interval remain coherent: three observations in
            # the selected interval, no fabricated independent bucket counts.
            s["previous"]["value"] = (1 << 53) - 4
            if i < NAMES.index(state):
                s["current"]["value"] = (1 << 53) - 4
            else:
                s.update(state="increased", delta=3)
                category = "increased"
        if state == "unknown_timestamps" and i == 12:
            s["previous"]["value"] -= 3
            s.update(state="increased", delta=3)
            category = "increased"
        if state == "sparse" and i < 12:
            s = {"signal": name, "state": "missing", "reason": "metric_missing"}
            counts["observed"] -= 1
            category = "missing"
        if state == "counter_reset" and i < 12:
            s["current"]["value"] = 1
            s.pop("delta")
            s.update(state="unavailable", reason="counter_reset")
            category = "unavailable"
        counts[category] += 1
        summaries[i][category if category in ("increased", "unchanged", "baseline") else "unknown"] += 1
        h["items"][0]["signals"].append(s)
    if counts["increased"]:
        h.update(state="increased", reason="cron_timing_increases_observed")
    if counts["missing"] + counts["unavailable"]:
        h.update(state="incomplete", reason="incomplete_cron_timing_evidence", incomplete=True)
    assert ObsRuntimeCommandCronTiming.from_dict(h).to_dict() == h
    payload = {
        "generated_at": now,
        "scope": "selected_critical_daemon_builds",
        "expected_role": "control-plane-v2",
        "requested_release": "b" * 40,
        "evidence_after": "2026-11-16T12:59:00+00:00",
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
        "command_cron_timing": h,
    }
    parsed = ObsRuntimeBuilds.from_dict(payload)
    assert isinstance(parsed.command_cron_timing, ObsRuntimeCommandCronTiming)
    assert parsed.to_dict() == payload
