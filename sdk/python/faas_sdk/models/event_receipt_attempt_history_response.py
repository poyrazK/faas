from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_receipt_attempt_history_response_coverage import (
    EventReceiptAttemptHistoryResponseCoverage,
    check_event_receipt_attempt_history_response_coverage,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.invocation_attempt_response import InvocationAttemptResponse


T = TypeVar("T", bound="EventReceiptAttemptHistoryResponse")


@_attrs_define
class EventReceiptAttemptHistoryResponse:
    """Retained dispatch attempts for the original application event delivery and its trusted replay descendants."""

    event_source: str
    event_id: str
    subscription_id: str
    original_invocation_id: UUID
    coverage: EventReceiptAttemptHistoryResponseCoverage
    """History is not backfilled and may be pruned; absence is not proof of no delivery."""
    attempts: list[InvocationAttemptResponse]
    next_after: str | Unset = UNSET
    """Opaque cursor for the next page of older dispatch attempts for this captured recipient."""

    def to_dict(self) -> dict[str, Any]:
        event_source = self.event_source

        event_id = self.event_id

        subscription_id = self.subscription_id

        original_invocation_id = str(self.original_invocation_id)

        coverage: str = self.coverage

        attempts = []
        for attempts_item_data in self.attempts:
            attempts_item = attempts_item_data.to_dict()
            attempts.append(attempts_item)

        next_after = self.next_after

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_source": event_source,
                "event_id": event_id,
                "subscription_id": subscription_id,
                "original_invocation_id": original_invocation_id,
                "coverage": coverage,
                "attempts": attempts,
            }
        )
        if next_after is not UNSET:
            field_dict["next_after"] = next_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.invocation_attempt_response import InvocationAttemptResponse

        d = dict(src_dict)
        event_source = d.pop("event_source")

        event_id = d.pop("event_id")

        subscription_id = d.pop("subscription_id")

        original_invocation_id = UUID(d.pop("original_invocation_id"))

        coverage = check_event_receipt_attempt_history_response_coverage(d.pop("coverage"))

        attempts = []
        _attempts = d.pop("attempts")
        for attempts_item_data in _attempts:
            attempts_item = InvocationAttemptResponse.from_dict(attempts_item_data)

            attempts.append(attempts_item)

        next_after = d.pop("next_after", UNSET)

        event_receipt_attempt_history_response = cls(
            event_source=event_source,
            event_id=event_id,
            subscription_id=subscription_id,
            original_invocation_id=original_invocation_id,
            coverage=coverage,
            attempts=attempts,
            next_after=next_after,
        )

        return event_receipt_attempt_history_response
