from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ConnectionBudgetInventory")


@_attrs_define
class ConnectionBudgetInventory:
    """Deduplicated app/scope potential consumers; counts are not live SQL sessions."""

    bindings: int
    data_api_consumers: int
    auth_consumers: int
    migration_bindings: int
    other_bindings: int
    resident_instances: int
    parked_instances: int
    auxiliary_instances: int
    active_tasks: int
    rotation_pending: int
    overlap_consumers: int
    stale_node_instances: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        bindings = self.bindings

        data_api_consumers = self.data_api_consumers

        auth_consumers = self.auth_consumers

        migration_bindings = self.migration_bindings

        other_bindings = self.other_bindings

        resident_instances = self.resident_instances

        parked_instances = self.parked_instances

        auxiliary_instances = self.auxiliary_instances

        active_tasks = self.active_tasks

        rotation_pending = self.rotation_pending

        overlap_consumers = self.overlap_consumers

        stale_node_instances = self.stale_node_instances

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "bindings": bindings,
                "data_api_consumers": data_api_consumers,
                "auth_consumers": auth_consumers,
                "migration_bindings": migration_bindings,
                "other_bindings": other_bindings,
                "resident_instances": resident_instances,
                "parked_instances": parked_instances,
                "auxiliary_instances": auxiliary_instances,
                "active_tasks": active_tasks,
                "rotation_pending": rotation_pending,
                "overlap_consumers": overlap_consumers,
                "stale_node_instances": stale_node_instances,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        bindings = d.pop("bindings")

        data_api_consumers = d.pop("data_api_consumers")

        auth_consumers = d.pop("auth_consumers")

        migration_bindings = d.pop("migration_bindings")

        other_bindings = d.pop("other_bindings")

        resident_instances = d.pop("resident_instances")

        parked_instances = d.pop("parked_instances")

        auxiliary_instances = d.pop("auxiliary_instances")

        active_tasks = d.pop("active_tasks")

        rotation_pending = d.pop("rotation_pending")

        overlap_consumers = d.pop("overlap_consumers")

        stale_node_instances = d.pop("stale_node_instances")

        connection_budget_inventory = cls(
            bindings=bindings,
            data_api_consumers=data_api_consumers,
            auth_consumers=auth_consumers,
            migration_bindings=migration_bindings,
            other_bindings=other_bindings,
            resident_instances=resident_instances,
            parked_instances=parked_instances,
            auxiliary_instances=auxiliary_instances,
            active_tasks=active_tasks,
            rotation_pending=rotation_pending,
            overlap_consumers=overlap_consumers,
            stale_node_instances=stale_node_instances,
        )

        connection_budget_inventory.additional_properties = d
        return connection_budget_inventory

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
