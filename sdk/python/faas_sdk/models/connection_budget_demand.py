from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ConnectionBudgetDemand")


@_attrs_define
class ConnectionBudgetDemand:
    """Canonical runtime pools and explicit planning headroom. Unbounded consumers remain unknown."""

    data_api_resident_estimate: int
    data_api_wake_estimate: int
    auth_wake_estimate: int
    control_headroom: int
    migration_headroom: int
    modeled_peak_connections: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        data_api_resident_estimate = self.data_api_resident_estimate

        data_api_wake_estimate = self.data_api_wake_estimate

        auth_wake_estimate = self.auth_wake_estimate

        control_headroom = self.control_headroom

        migration_headroom = self.migration_headroom

        modeled_peak_connections = self.modeled_peak_connections

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "data_api_resident_estimate": data_api_resident_estimate,
                "data_api_wake_estimate": data_api_wake_estimate,
                "auth_wake_estimate": auth_wake_estimate,
                "control_headroom": control_headroom,
                "migration_headroom": migration_headroom,
                "modeled_peak_connections": modeled_peak_connections,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        data_api_resident_estimate = d.pop("data_api_resident_estimate")

        data_api_wake_estimate = d.pop("data_api_wake_estimate")

        auth_wake_estimate = d.pop("auth_wake_estimate")

        control_headroom = d.pop("control_headroom")

        migration_headroom = d.pop("migration_headroom")

        modeled_peak_connections = d.pop("modeled_peak_connections")

        connection_budget_demand = cls(
            data_api_resident_estimate=data_api_resident_estimate,
            data_api_wake_estimate=data_api_wake_estimate,
            auth_wake_estimate=auth_wake_estimate,
            control_headroom=control_headroom,
            migration_headroom=migration_headroom,
            modeled_peak_connections=modeled_peak_connections,
        )

        connection_budget_demand.additional_properties = d
        return connection_budget_demand

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
