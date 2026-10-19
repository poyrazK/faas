from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.connection_budget_capacity_source import (
    ConnectionBudgetCapacitySource,
    check_connection_budget_capacity_source,
)
from ..models.connection_budget_capacity_state import (
    ConnectionBudgetCapacityState,
    check_connection_budget_capacity_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ConnectionBudgetCapacity")


@_attrs_define
class ConnectionBudgetCapacity:
    """Operator-qualified tenant allocation clamped to observed SQL ceilings for the exact database generation and
    placement. Unknown without fresh evidence; this does not reserve connections or establish complete consumer
    coverage.

    """

    state: ConnectionBudgetCapacityState
    usable_connections: int | Unset = UNSET
    source: ConnectionBudgetCapacitySource | Unset = UNSET
    checked_at: datetime.datetime | Unset = UNSET
    expires_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        usable_connections = self.usable_connections

        source: str | Unset = UNSET
        if not isinstance(self.source, Unset):
            source = self.source

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "state": state,
            }
        )
        if usable_connections is not UNSET:
            field_dict["usable_connections"] = usable_connections
        if source is not UNSET:
            field_dict["source"] = source
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_connection_budget_capacity_state(d.pop("state"))

        usable_connections = d.pop("usable_connections", UNSET)

        _source = d.pop("source", UNSET)
        source: ConnectionBudgetCapacitySource | Unset
        if isinstance(_source, Unset):
            source = UNSET
        else:
            source = check_connection_budget_capacity_source(_source)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        connection_budget_capacity = cls(
            state=state,
            usable_connections=usable_connections,
            source=source,
            checked_at=checked_at,
            expires_at=expires_at,
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
