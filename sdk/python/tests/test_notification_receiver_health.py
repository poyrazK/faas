"""Receiver evidence preserves unknown values and compatibility with older servers."""

import pytest

from faas_sdk.preview.models.obs_notification_health import ObsNotificationHealth
from faas_sdk.types import UNSET


def legacy_snapshot() -> dict:
    return {
        "generated_at": "2026-11-13T10:00:00+00:00",
        "state": "healthy",
        "window_seconds": 600,
        "alertmanager_api": {"state": "healthy"},
        "signals": {},
        "integrations": [],
        "limitations": ["aggregate_integration_counters"],
    }


def signal(value: float | None, window: int = 0) -> dict:
    return {
        "state": "healthy" if value is not None else "no_samples",
        **({"reason": "success_not_observed"} if value is None else {}),
        "value": value,
        "observed_at": "2026-11-13T10:00:00+00:00",
        "window_seconds": window,
        "max_age_seconds": 120,
    }


def receiver_snapshot() -> dict:
    body = legacy_snapshot()
    body["receiver_health"] = {
        "state": "partial",
        "reason": "incomplete_evidence",
        "collector": signal(1),
        "inventory": signal(1),
        "groups": [
            {
                "receiver": "faas-page",
                "integration": "slack",
                "expected": True,
                "state": "partial",
                "reason": "incomplete_evidence",
                "requests": signal(0, 600),
                "failures": signal(0, 600),
                "last_success_observation": signal(None),
            }
        ],
        "limitations": ["success_clock_is_confirmation_time", "no_human_receipt_confirmation"],
    }
    return body


def test_old_server_response_omits_receiver_section_without_fabricating_it() -> None:
    body = legacy_snapshot()
    decoded = ObsNotificationHealth.from_dict(body)
    assert decoded.receiver_health is UNSET
    assert decoded.to_dict() == body


def test_fresh_zero_counters_and_unknown_receipt_remain_distinct() -> None:
    body = receiver_snapshot()
    decoded = ObsNotificationHealth.from_dict(body)
    receivers = decoded.receiver_health
    assert receivers is not UNSET
    group = receivers.groups[0]
    assert group.expected is True
    assert group.requests.value == group.failures.value == 0
    assert group.last_success_observation.value is None
    assert decoded.to_dict() == body


def test_missing_inventory_preserves_null_expected_without_inferring_false() -> None:
    body = receiver_snapshot()
    body["receiver_health"]["inventory"] = {
        "state": "unavailable",
        "reason": "inventory_missing",
        "value": None,
        "window_seconds": 0,
        "max_age_seconds": 120,
    }
    body["receiver_health"]["groups"][0]["expected"] = None
    decoded = ObsNotificationHealth.from_dict(body)
    assert decoded.receiver_health.groups[0].expected is None
    assert decoded.to_dict() == body


@pytest.mark.parametrize("field,value", [("receiver", "private-role"), ("integration", "private-provider")])
def test_unknown_receiver_metadata_does_not_decode_as_a_reviewed_role(field: str, value: str) -> None:
    body = receiver_snapshot()
    body["receiver_health"]["groups"][0][field] = value
    with pytest.raises(TypeError):
        ObsNotificationHealth.from_dict(body)
