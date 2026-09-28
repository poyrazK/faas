"""Verify signed outbound Gregale webhook requests."""

from __future__ import annotations

import hashlib
import hmac
import re
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from typing import Literal

WEBHOOK_SIGNATURE_HEADER = "X-Faas-Webhook-Signature"
WEBHOOK_TIMESTAMP_HEADER = "X-Faas-Webhook-Timestamp"
WEBHOOK_DELIVERY_ID_HEADER = "X-Faas-Delivery-Id"
DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE = timedelta(minutes=5)

WebhookVerificationErrorCode = Literal[
    "missing_header",
    "malformed_header",
    "malformed_signature",
    "stale_timestamp",
    "bad_signature",
    "invalid_secret",
    "invalid_body",
    "invalid_options",
]


class WebhookVerificationError(ValueError):
    """Safe-to-log verification failure; messages omit secret and request data."""

    def __init__(self, code: WebhookVerificationErrorCode) -> None:
        self.code = code
        super().__init__(f"webhook verification: {code}")


@dataclass(frozen=True)
class VerifiedWebhook:
    """Authenticated metadata for one Gregale delivery.

    ``delivery_id`` remains stable across automatic retries. Persist it with
    the handler's business change to deduplicate retries; checking the
    signature alone does not prevent replay within the timestamp window.
    """

    delivery_id: str
    timestamp: datetime


def verify_webhook(
    secret: str | bytes,
    headers: Mapping[str, str | Sequence[str]],
    body: bytes | bytearray | memoryview,
    *,
    timestamp_tolerance: timedelta = DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE,
    now: datetime | None = None,
) -> VerifiedWebhook:
    """Verify the raw body and Gregale signature headers.

    Pass the exact request bytes before parsing or re-serializing JSON. The
    signature is HMAC-SHA256 over
    ``<unix_timestamp>.<delivery_id>.<raw_body>``. The default accepted clock
    skew is five minutes. Persist the returned ``delivery_id`` transactionally
    with handler side effects to deduplicate retries.
    """
    key = secret.encode("utf-8") if isinstance(secret, str) else secret
    if not isinstance(key, bytes) or not key:
        raise WebhookVerificationError("invalid_secret")
    if not isinstance(body, (bytes, bytearray, memoryview)):
        raise WebhookVerificationError("invalid_body")
    if timestamp_tolerance < timedelta(0):
        raise WebhookVerificationError("invalid_options")
    tolerance = timestamp_tolerance or DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE
    current = now or datetime.now(timezone.utc)
    if current.tzinfo is None or current.utcoffset() is None:
        raise WebhookVerificationError("invalid_options")

    signature_header = _single_header(headers, WEBHOOK_SIGNATURE_HEADER)
    timestamp_header = _single_header(headers, WEBHOOK_TIMESTAMP_HEADER)
    delivery_id = _single_header(headers, WEBHOOK_DELIVERY_ID_HEADER)
    if (
        delivery_id.strip() != delivery_id
        or any(char in delivery_id for char in ",\r\n")
        or len(delivery_id.encode("utf-8")) > 256
    ):
        raise WebhookVerificationError("malformed_header")

    if re.fullmatch(r"(?:0|[1-9][0-9]*)", timestamp_header) is None:
        raise WebhookVerificationError("malformed_header")
    timestamp_seconds = int(timestamp_header)
    if str(timestamp_seconds) != timestamp_header:
        raise WebhookVerificationError("malformed_header")
    if abs(current.timestamp() - timestamp_seconds) > tolerance.total_seconds():
        raise WebhookVerificationError("stale_timestamp")

    match = re.fullmatch(r"sha256=([0-9a-fA-F]{64})", signature_header)
    if match is None:
        raise WebhookVerificationError("malformed_signature")
    supplied = bytes.fromhex(match.group(1))
    canonical = timestamp_header.encode("ascii") + b"." + delivery_id.encode("utf-8") + b"." + bytes(body)
    expected = hmac.new(key, canonical, hashlib.sha256).digest()
    if not hmac.compare_digest(supplied, expected):
        raise WebhookVerificationError("bad_signature")

    return VerifiedWebhook(
        delivery_id=delivery_id,
        timestamp=datetime.fromtimestamp(timestamp_seconds, timezone.utc),
    )


def _single_header(headers: Mapping[str, str | Sequence[str]], name: str) -> str:
    values: list[str] = []
    found = False
    for key, value in headers.items():
        if not isinstance(key, str) or key.casefold() != name.casefold():
            continue
        found = True
        if isinstance(value, str):
            values.append(value)
        elif isinstance(value, Sequence):
            values.extend(item for item in value if isinstance(item, str))
            if any(not isinstance(item, str) for item in value):
                raise WebhookVerificationError("malformed_header")
        else:
            raise WebhookVerificationError("malformed_header")
    if not found:
        raise WebhookVerificationError("missing_header")
    if len(values) != 1 or not values[0]:
        raise WebhookVerificationError("malformed_header")
    return values[0]
