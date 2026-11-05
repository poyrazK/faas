from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.app_event_publication_verification_acceptance import (
    AppEventPublicationVerificationAcceptance,
    check_app_event_publication_verification_acceptance,
)
from ..models.app_event_publication_verification_reason import (
    AppEventPublicationVerificationReason,
    check_app_event_publication_verification_reason,
)
from ..models.app_event_publication_verification_status import (
    AppEventPublicationVerificationStatus,
    check_app_event_publication_verification_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.publish_event_response import PublishEventResponse


T = TypeVar("T", bound="AppEventPublicationVerification")


@_attrs_define
class AppEventPublicationVerification:
    """Snapshot comparison of intended content with an existing retained publication."""

    app_id: UUID
    source: str
    event_id: str
    observed_at: datetime.datetime
    status: AppEventPublicationVerificationStatus
    """Whether retained normalized type, schema version and JSON data match the supplied intent."""
    receipt_url: str
    expected_accepted_at: datetime.datetime | Unset = UNSET
    """Optional requested acceptance instant normalized to UTC without precision loss."""
    acceptance: AppEventPublicationVerificationAcceptance | Unset = UNSET
    """Acceptance timestamp comparison present only when the caller supplies a guard."""
    reason: AppEventPublicationVerificationReason | Unset = UNSET
    """Verification could not observe a retained identity; nonpublication remains unproven."""
    receipt: PublishEventResponse | Unset = UNSET
    """Durable acceptance receipt for a published internal event."""

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        source = self.source

        event_id = self.event_id

        observed_at = self.observed_at.isoformat()

        status: str = self.status

        receipt_url = self.receipt_url

        expected_accepted_at: str | Unset = UNSET
        if not isinstance(self.expected_accepted_at, Unset):
            expected_accepted_at = self.expected_accepted_at.isoformat()

        acceptance: str | Unset = UNSET
        if not isinstance(self.acceptance, Unset):
            acceptance = self.acceptance

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        receipt: dict[str, Any] | Unset = UNSET
        if not isinstance(self.receipt, Unset):
            receipt = self.receipt.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "source": source,
                "event_id": event_id,
                "observed_at": observed_at,
                "status": status,
                "receipt_url": receipt_url,
            }
        )
        if expected_accepted_at is not UNSET:
            field_dict["expected_accepted_at"] = expected_accepted_at
        if acceptance is not UNSET:
            field_dict["acceptance"] = acceptance
        if reason is not UNSET:
            field_dict["reason"] = reason
        if receipt is not UNSET:
            field_dict["receipt"] = receipt

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.publish_event_response import PublishEventResponse

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        source = d.pop("source")

        event_id = d.pop("event_id")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        status = check_app_event_publication_verification_status(d.pop("status"))

        receipt_url = d.pop("receipt_url")

        _expected_accepted_at = d.pop("expected_accepted_at", UNSET)
        expected_accepted_at: datetime.datetime | Unset
        if isinstance(_expected_accepted_at, Unset):
            expected_accepted_at = UNSET
        else:
            expected_accepted_at = datetime.datetime.fromisoformat(_expected_accepted_at)

        _acceptance = d.pop("acceptance", UNSET)
        acceptance: AppEventPublicationVerificationAcceptance | Unset
        if isinstance(_acceptance, Unset):
            acceptance = UNSET
        else:
            acceptance = check_app_event_publication_verification_acceptance(_acceptance)

        _reason = d.pop("reason", UNSET)
        reason: AppEventPublicationVerificationReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_app_event_publication_verification_reason(_reason)

        _receipt = d.pop("receipt", UNSET)
        receipt: PublishEventResponse | Unset
        if isinstance(_receipt, Unset):
            receipt = UNSET
        else:
            receipt = PublishEventResponse.from_dict(_receipt)

        app_event_publication_verification = cls(
            app_id=app_id,
            source=source,
            event_id=event_id,
            observed_at=observed_at,
            status=status,
            receipt_url=receipt_url,
            expected_accepted_at=expected_accepted_at,
            acceptance=acceptance,
            reason=reason,
            receipt=receipt,
        )

        return app_event_publication_verification
