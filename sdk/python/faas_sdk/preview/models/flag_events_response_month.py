from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="FlagEventsResponseMonth")


@_attrs_define
class FlagEventsResponseMonth:
    limit: int
    accepted: int
    dropped: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        limit = self.limit

        accepted = self.accepted

        dropped = self.dropped

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "limit": limit,
                "accepted": accepted,
                "dropped": dropped,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        limit = d.pop("limit")

        accepted = d.pop("accepted")

        dropped = d.pop("dropped")

        flag_events_response_month = cls(
            limit=limit,
            accepted=accepted,
            dropped=dropped,
        )

        flag_events_response_month.additional_properties = d
        return flag_events_response_month

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
