from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_ai_price_entry import OutboundAIPriceEntry


T = TypeVar("T", bound="OutboundAIPriceListResponse")


@_attrs_define
class OutboundAIPriceListResponse:
    """The price list in effect; effective_from is null when none was ever set. Models the list does not name are priced
    from the built-in catalog.

    """

    effective_from: datetime.datetime | None
    entries: list[OutboundAIPriceEntry]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        effective_from: None | str
        if isinstance(self.effective_from, datetime.datetime):
            effective_from = self.effective_from.isoformat()
        else:
            effective_from = self.effective_from

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "effective_from": effective_from,
                "entries": entries,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_price_entry import OutboundAIPriceEntry

        d = dict(src_dict)

        def _parse_effective_from(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                effective_from_type_0 = datetime.datetime.fromisoformat(data)

                return effective_from_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        effective_from = _parse_effective_from(d.pop("effective_from"))

        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = OutboundAIPriceEntry.from_dict(entries_item_data)

            entries.append(entries_item)

        outbound_ai_price_list_response = cls(
            effective_from=effective_from,
            entries=entries,
        )

        outbound_ai_price_list_response.additional_properties = d
        return outbound_ai_price_list_response

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
