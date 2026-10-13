from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_ai_request_log_entry import OutboundAIRequestLogEntry


T = TypeVar("T", bound="OutboundAIRequestLogList")


@_attrs_define
class OutboundAIRequestLogList:
    """One newest-first page of the AI request log."""

    items: list[OutboundAIRequestLogEntry]
    next_before: datetime.datetime | None
    """Pass as before to read the next page; null on the last page."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        next_before: None | str
        if isinstance(self.next_before, datetime.datetime):
            next_before = self.next_before.isoformat()
        else:
            next_before = self.next_before

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
                "next_before": next_before,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_request_log_entry import OutboundAIRequestLogEntry

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = OutboundAIRequestLogEntry.from_dict(items_item_data)

            items.append(items_item)

        def _parse_next_before(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                next_before_type_0 = datetime.datetime.fromisoformat(data)

                return next_before_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        next_before = _parse_next_before(d.pop("next_before"))

        outbound_ai_request_log_list = cls(
            items=items,
            next_before=next_before,
        )

        outbound_ai_request_log_list.additional_properties = d
        return outbound_ai_request_log_list

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
