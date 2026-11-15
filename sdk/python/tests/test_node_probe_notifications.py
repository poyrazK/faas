"""A direct node lookup preserves intent unknowns and completed submission history."""

from faas_sdk.preview.models.obs_node_probe_notifications import ObsNodeProbeNotifications

from faas_sdk.types import UNSET


def snapshot() -> dict:
    return {
        "generated_at": "2026-11-14T01:00:00+00:00",
        "scope": "node_serving_probe_notification_submissions",
        "node": "node-0039",
        "state": "unknown",
        "reason": "inventory_unavailable",
        "max_age_seconds": 120,
        "inventory": {
            "state": "unavailable",
            "reason": "inventory_missing",
            "configured": None,
            "expected_pairs": None,
            "observed_at": None,
        },
        "expected": None,
        "max_result_age_seconds": None,
        "channels": [],
    }


def configured_inventory(body: dict) -> dict:
    return {
        "state": "healthy",
        "configured": True,
        "expected_pairs": 40,
        "observed_at": body["generated_at"],
    }


def test_unknown_inventory_keeps_expectation_null() -> None:
    body = snapshot()
    decoded = ObsNodeProbeNotifications.from_dict(body)
    assert decoded.expected is None
    assert decoded.max_result_age_seconds is None
    assert decoded.channels == []
    assert decoded.to_dict() == body


def test_node_outside_configured_intent_keeps_false_distinct_from_unknown() -> None:
    body = snapshot()
    body.update(state="not_applicable", reason="node_not_expected", expected=False)
    body["inventory"] = configured_inventory(body)
    decoded = ObsNodeProbeNotifications.from_dict(body)
    assert decoded.expected is False
    assert decoded.channels == []
    assert decoded.to_dict() == body


def test_expected_node_preserves_both_channels_and_historical_success() -> None:
    body = snapshot()
    body.update(state="partial", reason="incomplete_evidence", expected=True, max_result_age_seconds=900)
    body["inventory"] = configured_inventory(body)
    body["channels"] = [
        {
            "node": body["node"],
            "channel": "webhook",
            "state": "last_submission_failed",
            "enabled": True,
            "evaluated_at": body["generated_at"],
            "last_run_at": body["generated_at"],
            "last_attempt_at": "2026-11-13T12:00:00+00:00",
            "last_success_at": "2026-11-12T12:00:00+00:00",
            "failure_code": "timeout",
        },
        {
            "node": body["node"],
            "channel": "heartbeat",
            "state": "unknown",
            "reason": "recording_unknown",
            "enabled": None,
            "evaluated_at": body["generated_at"],
            "last_run_at": None,
            "last_attempt_at": None,
            "last_success_at": None,
        },
    ]
    decoded = ObsNodeProbeNotifications.from_dict(body)
    assert decoded.expected is True
    assert {channel.channel for channel in decoded.channels} == {"heartbeat", "webhook"}
    assert all(channel.node == "node-0039" for channel in decoded.channels)
    assert decoded.channels[0].last_success_at.isoformat() == "2026-11-12T12:00:00+00:00"
    assert decoded.channels[1].last_success_at is None
    assert decoded.channels[1].failure_code is UNSET
    assert decoded.to_dict() == body
