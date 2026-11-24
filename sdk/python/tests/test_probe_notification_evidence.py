"""Unknown notification outcomes retain a bounded evidence cause and nullable history."""

import pytest

from faas_sdk.preview.models.obs_node_probe_notifications import ObsNodeProbeNotifications
from faas_sdk.preview.models.obs_serving_health import ObsServingHealth


@pytest.mark.parametrize("reason", ["no_submission_yet", "telemetry_missing", "telemetry_invalid", "source_unavailable"])
def test_notification_evidence_reason_round_trips_in_fleet_and_node(reason: str) -> None:
    generated = "2026-11-14T02:00:00+00:00"
    inventory = {"state": "healthy", "configured": True, "expected_pairs": 1, "observed_at": generated}
    channels = [
        {"node": "fsn-3", "channel": channel, "state": "unknown", "reason": "recording_unknown", "evidence_reason": reason,
         "enabled": None, "evaluated_at": generated, "last_run_at": None,
         "last_attempt_at": None, "last_success_at": None}
        for channel in ("heartbeat", "webhook")
    ]
    node = {
        "generated_at": generated, "scope": "node_serving_probe_notification_submissions", "node": "fsn-3",
        "state": "unknown", "reason": "incomplete_evidence", "max_age_seconds": 120,
        "inventory": inventory, "expected": True, "max_result_age_seconds": 900, "channels": channels,
    }
    fleet = {
        "generated_at": generated, "scope": "expected_serving_probe_coverage", "state": "partial",
        "reason": "incomplete_evidence", "max_age_seconds": 120, "inventory": inventory,
        "counts": None, "probes": [], "limitations": [],
        "probe_notifications": {
            "state": "unknown", "reason": "incomplete_evidence", "expected_channels": 2,
            "counts": {"disabled": 0, "unknown": 2, "last_submission_succeeded": 0, "last_submission_failed": 0},
            "limit": 32, "omitted": 0, "truncated": False, "channels": channels,
        },
    }
    decoded_node = ObsNodeProbeNotifications.from_dict(node)
    decoded_fleet = ObsServingHealth.from_dict(fleet)
    assert decoded_node.to_dict() == node
    assert decoded_fleet.to_dict() == fleet
    for items in (decoded_node.channels, decoded_fleet.probe_notifications.channels):
        assert all(item.reason == "recording_unknown" and item.evidence_reason == reason
                   and item.last_attempt_at is None and item.last_success_at is None for item in items)
