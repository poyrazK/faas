from __future__ import annotations

import hashlib
import hmac
from datetime import UTC, datetime, timedelta
from time import time
from uuid import UUID

import pytest

from faas_sdk import (
    DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE,
    WEBHOOK_DELIVERY_ID_HEADER,
    WEBHOOK_SIGNATURE_HEADER,
    WEBHOOK_TIMESTAMP_HEADER,
    WebhookVerificationError,
    verify_webhook,
)
from faas_sdk.models import CreateAppWebhookRequest, WorkflowFinishedWebhookPayload

SECRET = "whsec_test_123"
TIMESTAMP = 1_712_345_678
DELIVERY_ID = "delivery-123"
BODY = b'{"type":"invoice.paid","amount":42}'
SIGNATURE = "sha256=9733751b9a5946bb55cb0f75a16ae54fa21f3d6e827284736a9a5cdf4b07e6d8"
NOW = datetime.fromtimestamp(TIMESTAMP, UTC)


def _headers(**overrides: str | list[str]) -> dict[str, str | list[str]]:
    return {
        WEBHOOK_SIGNATURE_HEADER: SIGNATURE,
        WEBHOOK_TIMESTAMP_HEADER: str(TIMESTAMP),
        WEBHOOK_DELIVERY_ID_HEADER: DELIVERY_ID,
        **overrides,
    }


def _sign(timestamp: int, delivery_id: str, body: bytes) -> str:
    canonical = f"{timestamp}.{delivery_id}.".encode() + body
    digest = hmac.new(SECRET.encode(), canonical, hashlib.sha256).hexdigest()
    return f"sha256={digest}"


def test_verifies_gregale_golden_signature_and_returns_stable_delivery_id() -> None:
    verified = verify_webhook(SECRET, _headers(), BODY, now=NOW)
    assert verified.delivery_id == DELIVERY_ID
    assert verified.timestamp == NOW


def test_uses_default_clock_and_timestamp_tolerance() -> None:
    current_timestamp = int(time())
    current_body = b"current event"
    headers = _headers(
        **{
            WEBHOOK_TIMESTAMP_HEADER: str(current_timestamp),
            WEBHOOK_SIGNATURE_HEADER: _sign(current_timestamp, DELIVERY_ID, current_body),
        }
    )
    verified = verify_webhook(SECRET, headers, current_body)
    assert verified.delivery_id == DELIVERY_ID


def test_rejects_changed_raw_body_or_wrong_secret() -> None:
    with pytest.raises(WebhookVerificationError, match="bad_signature"):
        verify_webhook(SECRET, _headers(), BODY + b" ", now=NOW)
    with pytest.raises(WebhookVerificationError, match="bad_signature"):
        verify_webhook("wrong-secret", _headers(), BODY, now=NOW)


@pytest.mark.parametrize(
    ("timestamp", "now"),
    [
        (TIMESTAMP, NOW + DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE + timedelta(seconds=1)),
        (TIMESTAMP + int(DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE.total_seconds()) + 1, NOW),
    ],
)
def test_rejects_stale_and_future_timestamps(timestamp: int, now: datetime) -> None:
    headers = _headers(
        **{
            WEBHOOK_TIMESTAMP_HEADER: str(timestamp),
            WEBHOOK_SIGNATURE_HEADER: _sign(timestamp, DELIVERY_ID, BODY),
        }
    )
    with pytest.raises(WebhookVerificationError, match="stale_timestamp"):
        verify_webhook(SECRET, headers, BODY, now=now)


def test_rejects_missing_duplicate_and_malformed_headers() -> None:
    missing = _headers()
    del missing[WEBHOOK_DELIVERY_ID_HEADER]
    with pytest.raises(WebhookVerificationError, match="missing_header"):
        verify_webhook(SECRET, missing, BODY, now=NOW)

    duplicate = _headers()
    duplicate["x-faas-delivery-id"] = DELIVERY_ID
    with pytest.raises(WebhookVerificationError, match="malformed_header"):
        verify_webhook(SECRET, duplicate, BODY, now=NOW)

    with pytest.raises(WebhookVerificationError, match="malformed_header"):
        verify_webhook(
            SECRET,
            _headers(**{WEBHOOK_SIGNATURE_HEADER: [SIGNATURE, SIGNATURE]}),
            BODY,
            now=NOW,
        )
    with pytest.raises(WebhookVerificationError, match="malformed_signature"):
        verify_webhook(SECRET, _headers(**{WEBHOOK_SIGNATURE_HEADER: "sha256=not-hex"}), BODY, now=NOW)
    with pytest.raises(WebhookVerificationError, match="malformed_header"):
        verify_webhook(SECRET, _headers(**{WEBHOOK_TIMESTAMP_HEADER: "01712345678"}), BODY, now=NOW)


def test_rejects_empty_secret_negative_tolerance_and_naive_clock() -> None:
    with pytest.raises(WebhookVerificationError, match="invalid_secret"):
        verify_webhook("", _headers(), BODY, now=NOW)
    with pytest.raises(WebhookVerificationError, match="invalid_options"):
        verify_webhook(SECRET, _headers(), BODY, timestamp_tolerance=-timedelta(seconds=1), now=NOW)
    with pytest.raises(WebhookVerificationError, match="invalid_options"):
        verify_webhook(SECRET, _headers(), BODY, now=datetime(2026, 1, 1))
    with pytest.raises(WebhookVerificationError, match="invalid_body"):
        verify_webhook(SECRET, _headers(), "body", now=NOW)  # type: ignore[arg-type]


def test_generated_sdk_supports_workflow_finished_subscription_and_payload() -> None:
    subscription = CreateAppWebhookRequest.from_dict(
        {
            "target_url": "https://example.com/gregale",
            "webhook_secret": "test-secret",
            "event_filter": ["workflow.finished"],
        }
    )
    assert subscription.event_filter == ["workflow.finished"]

    payload = WorkflowFinishedWebhookPayload.from_dict(
        {
            "app_id": "00000000-0000-0000-0000-000000000001",
            "run_id": "00000000-0000-0000-0000-000000000002",
            "workflow_name": "invoice-receipt",
            "status": "succeeded",
            "finished_at": "2026-10-05T12:30:00+00:00",
            "resume_count": 0,
        }
    )
    assert payload.app_id == UUID("00000000-0000-0000-0000-000000000001")
    assert payload.status == "succeeded"
    assert payload.to_dict()["resume_count"] == 0
