"""Both creation request models retain typed fixed and event protection selections."""

from datetime import UTC, datetime

from faas_sdk.models import (
    CreateObjectMultipartUploadRequest,
    ObjectRetentionPeriod,
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


def test_event_write_protection_requests() -> None:
    for retention in (
        ObjectVersionRetention(mode="COMPLIANCE", event_hold="ON", event_hold_duration=ObjectRetentionPeriod(days=30)),
        ObjectVersionRetention(mode="GOVERNANCE", event_hold="ON", event_hold_duration=ObjectRetentionPeriod(years=1)),
        ObjectVersionRetention(mode="COMPLIANCE", event_hold="OFF", retain_until_date=datetime(2027, 1, 2, tzinfo=UTC)),
    ):
        protection = ObjectWriteProtection(retention=retention)
        for request in (
            ObjectSignRequest(method="PUT", key="key", size_bytes=3, protection=protection),
            CreateObjectMultipartUploadRequest(key="key", size_bytes=3, protection=protection),
        ):
            encoded = request.to_dict()
            decoded = type(request).from_dict(encoded)
            assert isinstance(decoded.protection, ObjectWriteProtection)
            assert decoded.protection.to_dict() == protection.to_dict()
