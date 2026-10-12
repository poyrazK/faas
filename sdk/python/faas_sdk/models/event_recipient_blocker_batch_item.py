from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_recipient_blocker_batch_item_error import (
    EventRecipientBlockerBatchItemError,
    check_event_recipient_blocker_batch_item_error,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_recipient_blocker_query import EventRecipientBlockerQuery
    from ..models.event_recipient_blocker_response import EventRecipientBlockerResponse


T = TypeVar("T", bound="EventRecipientBlockerBatchItem")


@_attrs_define
class EventRecipientBlockerBatchItem:
    """Exactly one of result or error is returned."""

    query: EventRecipientBlockerQuery
    """Event and consumer identity with the original acceptance instant for a read-only lookup."""
    result: EventRecipientBlockerResponse | Unset = UNSET
    """Current retained routing evidence for one acceptance-guarded consumer query."""
    error: EventRecipientBlockerBatchItemError | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        query = self.query.to_dict()

        result: dict[str, Any] | Unset = UNSET
        if not isinstance(self.result, Unset):
            result = self.result.to_dict()

        error: str | Unset = UNSET
        if not isinstance(self.error, Unset):
            error = self.error

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "query": query,
            }
        )
        if result is not UNSET:
            field_dict["result"] = result
        if error is not UNSET:
            field_dict["error"] = error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recipient_blocker_query import EventRecipientBlockerQuery
        from ..models.event_recipient_blocker_response import EventRecipientBlockerResponse

        d = dict(src_dict)
        query = EventRecipientBlockerQuery.from_dict(d.pop("query"))

        _result = d.pop("result", UNSET)
        result: EventRecipientBlockerResponse | Unset
        if isinstance(_result, Unset):
            result = UNSET
        else:
            result = EventRecipientBlockerResponse.from_dict(_result)

        _error = d.pop("error", UNSET)
        error: EventRecipientBlockerBatchItemError | Unset
        if isinstance(_error, Unset):
            error = UNSET
        else:
            error = check_event_recipient_blocker_batch_item_error(_error)

        event_recipient_blocker_batch_item = cls(
            query=query,
            result=result,
            error=error,
        )

        return event_recipient_blocker_batch_item
