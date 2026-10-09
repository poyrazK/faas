from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.app_event_publish_status_response_reason import (
    AppEventPublishStatusResponseReason,
    check_app_event_publish_status_response_reason,
)
from ..models.app_event_publish_status_response_status import (
    AppEventPublishStatusResponseStatus,
    check_app_event_publish_status_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_receipt_response import EventReceiptResponse
    from ..models.publish_event_response import PublishEventResponse


T = TypeVar("T", bound="AppEventPublishStatusResponse")


@_attrs_define
class AppEventPublishStatusResponse:
    """Read-only producer-key reconciliation with retained consumer evidence."""

    app_id: UUID
    source: str
    event_id: str
    observed_at: datetime.datetime
    status: AppEventPublishStatusResponseStatus
    """Routing progress of retained acceptance; never a consumer success claim."""
    receipt_url: str
    reason: AppEventPublishStatusResponseReason | Unset = UNSET
    """Missing receipt observation cannot establish nonexecution."""
    receipt: PublishEventResponse | Unset = UNSET
    """Durable acceptance receipt for a published internal event."""
    evidence: EventReceiptResponse | Unset = UNSET
    """Acceptance and routing evidence with a bounded page of captured and backfilled consumers; execution is a
    separate lifecycle."""

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        source = self.source

        event_id = self.event_id

        observed_at = self.observed_at.isoformat()

        status: str = self.status

        receipt_url = self.receipt_url

        reason: str | Unset = UNSET
        if not isinstance(self.reason, Unset):
            reason = self.reason

        receipt: dict[str, Any] | Unset = UNSET
        if not isinstance(self.receipt, Unset):
            receipt = self.receipt.to_dict()

        evidence: dict[str, Any] | Unset = UNSET
        if not isinstance(self.evidence, Unset):
            evidence = self.evidence.to_dict()

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
        if reason is not UNSET:
            field_dict["reason"] = reason
        if receipt is not UNSET:
            field_dict["receipt"] = receipt
        if evidence is not UNSET:
            field_dict["evidence"] = evidence

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_receipt_response import EventReceiptResponse
        from ..models.publish_event_response import PublishEventResponse

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        source = d.pop("source")

        event_id = d.pop("event_id")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        status = check_app_event_publish_status_response_status(d.pop("status"))

        receipt_url = d.pop("receipt_url")

        _reason = d.pop("reason", UNSET)
        reason: AppEventPublishStatusResponseReason | Unset
        if isinstance(_reason, Unset):
            reason = UNSET
        else:
            reason = check_app_event_publish_status_response_reason(_reason)

        _receipt = d.pop("receipt", UNSET)
        receipt: PublishEventResponse | Unset
        if isinstance(_receipt, Unset):
            receipt = UNSET
        else:
            receipt = PublishEventResponse.from_dict(_receipt)

        _evidence = d.pop("evidence", UNSET)
        evidence: EventReceiptResponse | Unset
        if isinstance(_evidence, Unset):
            evidence = UNSET
        else:
            evidence = EventReceiptResponse.from_dict(_evidence)

        app_event_publish_status_response = cls(
            app_id=app_id,
            source=source,
            event_id=event_id,
            observed_at=observed_at,
            status=status,
            receipt_url=receipt_url,
            reason=reason,
            receipt=receipt,
            evidence=evidence,
        )

        return app_event_publish_status_response
