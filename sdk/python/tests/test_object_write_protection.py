"""Both creation request models retain typed fixed protection selections."""

from datetime import UTC, datetime

from faas_sdk.models import (
    CreateObjectMultipartUploadRequest,
    ObjectSignRequest,
    ObjectVersionLegalHold,
    ObjectVersionRetention,
    ObjectWriteProtection,
)


def test_write_protection_requests() -> None:
    protection = ObjectWriteProtection(
        retention=ObjectVersionRetention(
            mode="COMPLIANCE",
            retain_until_date=datetime(2027, 1, 2, 3, 4, 5, 123000, tzinfo=UTC),
        ),
        legal_hold=ObjectVersionLegalHold(status="ON"),
    )
    requests = [
        ObjectSignRequest(method="PUT", key="key", size_bytes=3, protection=protection),
        CreateObjectMultipartUploadRequest(key="key", size_bytes=3, protection=protection),
    ]
    for request in requests:
        encoded = request.to_dict()
        assert encoded["protection"]["retention"]["retain_until_date"] == "2027-01-02T03:04:05.123000+00:00"
        assert encoded["protection"]["legal_hold"] == {"status": "ON"}
        decoded = type(request).from_dict(encoded)
        assert isinstance(decoded.protection, ObjectWriteProtection)
        assert decoded.protection.to_dict() == protection.to_dict()
