from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.connection_budget_capacity_state import (
    ConnectionBudgetCapacityState,
    check_connection_budget_capacity_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ConnectionBudgetCapacity")


@_attrs_define
class ConnectionBudgetCapacity:
    """Qualified tenant-usable allocation for the exact database generation and placement. Current adapters leave this
    unknown.

    """

    state: ConnectionBudgetCapacityState
    usable_connections: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        usable_connections = self.usable_connections

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
            }
        )
        if usable_connections is not UNSET:
            field_dict["usable_connections"] = usable_connections

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_connection_budget_capacity_state(d.pop("state"))

        usable_connections = d.pop("usable_connections", UNSET)

        connection_budget_capacity = cls(
            state=state,
            usable_connections=usable_connections,
        )

        connection_budget_capacity.additional_properties = d
        return connection_budget_capacity

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
