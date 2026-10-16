from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.event_recipient_blocker_query import EventRecipientBlockerQuery


T = TypeVar("T", bound="EventRecipientBlockerBatchRequest")


@_attrs_define
class EventRecipientBlockerBatchRequest:
    """Bounded exact-recipient queries, each guarded by the original acceptance instant."""

    queries: list[EventRecipientBlockerQuery]

    def to_dict(self) -> dict[str, Any]:
        queries = []
        for queries_item_data in self.queries:
            queries_item = queries_item_data.to_dict()
            queries.append(queries_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "queries": queries,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_recipient_blocker_query import EventRecipientBlockerQuery

        d = dict(src_dict)
        queries = []
        _queries = d.pop("queries")
        for queries_item_data in _queries:
            queries_item = EventRecipientBlockerQuery.from_dict(queries_item_data)

            queries.append(queries_item)

        event_recipient_blocker_batch_request = cls(
            queries=queries,
        )

        return event_recipient_blocker_batch_request
