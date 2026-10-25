from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.flag_request_evidence import FlagRequestEvidence


T = TypeVar("T", bound="FlagEvidencePage")


@_attrs_define
class FlagEvidencePage:
    """Stable window of up to 100 retained request aggregates; preserve filters for pagination."""

    items: list[FlagRequestEvidence]
    window_start: datetime.datetime
    window_end: datetime.datetime
    next_cursor: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        window_start = self.window_start.isoformat()

        window_end = self.window_end.isoformat()

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "items": items,
                "window_start": window_start,
                "window_end": window_end,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_request_evidence import FlagRequestEvidence

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = FlagRequestEvidence.from_dict(items_item_data)

            items.append(items_item)

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        window_end = datetime.datetime.fromisoformat(d.pop("window_end"))

        next_cursor = d.pop("next_cursor", UNSET)

        flag_evidence_page = cls(
            items=items,
            window_start=window_start,
            window_end=window_end,
            next_cursor=next_cursor,
        )

        flag_evidence_page.additional_properties = d
        return flag_evidence_page

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
