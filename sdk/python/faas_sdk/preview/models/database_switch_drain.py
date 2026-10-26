from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="DatabaseSwitchDrain")


@_attrs_define
class DatabaseSwitchDrain:
    """A fenced app's instances by outcome: retired with a native receipt, parked (no process; snapshot invalidated), still
    pending, or blocking the switch.

    """

    retired: int
    parked: int
    pending: int
    blocked: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        retired = self.retired

        parked = self.parked

        pending = self.pending

        blocked = self.blocked

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "retired": retired,
                "parked": parked,
                "pending": pending,
                "blocked": blocked,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        retired = d.pop("retired")

        parked = d.pop("parked")

        pending = d.pop("pending")

        blocked = d.pop("blocked")

        database_switch_drain = cls(
            retired=retired,
            parked=parked,
            pending=pending,
            blocked=blocked,
        )

        database_switch_drain.additional_properties = d
        return database_switch_drain

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
