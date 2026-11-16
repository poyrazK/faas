from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="FlagEventsResponseDropped")


@_attrs_define
class FlagEventsResponseDropped:
    quota: int
    """Over the monthly event quota."""
    unknown_customer: int
    """Not an active platform tenant of the account."""
    name_limit: int
    """A new event name over the environment's name bound."""
    invalid: int
    """Invalid name or value, or a time outside the accepted window."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        quota = self.quota

        unknown_customer = self.unknown_customer

        name_limit = self.name_limit

        invalid = self.invalid

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "quota": quota,
                "unknown_customer": unknown_customer,
                "name_limit": name_limit,
                "invalid": invalid,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        quota = d.pop("quota")

        unknown_customer = d.pop("unknown_customer")

        name_limit = d.pop("name_limit")

        invalid = d.pop("invalid")

        flag_events_response_dropped = cls(
            quota=quota,
            unknown_customer=unknown_customer,
            name_limit=name_limit,
            invalid=invalid,
        )

        flag_events_response_dropped.additional_properties = d
        return flag_events_response_dropped

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
