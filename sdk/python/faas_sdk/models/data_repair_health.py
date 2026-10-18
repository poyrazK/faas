from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_repair_health_item import DataRepairHealthItem
    from ..models.data_repair_phase_health import DataRepairPhaseHealth


T = TypeVar("T", bound="DataRepairHealth")


@_attrs_define
class DataRepairHealth:
    """Durable repair reservations and phase clocks; capacity is not measured host usage."""

    observed_at: datetime.datetime
    phases: list[DataRepairPhaseHealth]
    """All eight monitored phases, including zero counts."""
    held_slots: int
    held_memory_mb: int
    held_vcpus: int
    held_cpu_millicores: int
    unavailable_tasks: int
    items: list[DataRepairHealthItem]
    limit: int
    next_cursor: str | Unset = UNSET
    """Omitted on the last page."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        phases = []
        for phases_item_data in self.phases:
            phases_item = phases_item_data.to_dict()
            phases.append(phases_item)

        held_slots = self.held_slots

        held_memory_mb = self.held_memory_mb

        held_vcpus = self.held_vcpus

        held_cpu_millicores = self.held_cpu_millicores

        unavailable_tasks = self.unavailable_tasks

        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        limit = self.limit

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "observed_at": observed_at,
                "phases": phases,
                "held_slots": held_slots,
                "held_memory_mb": held_memory_mb,
                "held_vcpus": held_vcpus,
                "held_cpu_millicores": held_cpu_millicores,
                "unavailable_tasks": unavailable_tasks,
                "items": items,
                "limit": limit,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_repair_health_item import DataRepairHealthItem
        from ..models.data_repair_phase_health import DataRepairPhaseHealth

        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        phases = []
        _phases = d.pop("phases")
        for phases_item_data in _phases:
            phases_item = DataRepairPhaseHealth.from_dict(phases_item_data)

            phases.append(phases_item)

        held_slots = d.pop("held_slots")

        held_memory_mb = d.pop("held_memory_mb")

        held_vcpus = d.pop("held_vcpus")

        held_cpu_millicores = d.pop("held_cpu_millicores")

        unavailable_tasks = d.pop("unavailable_tasks")

        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = DataRepairHealthItem.from_dict(items_item_data)

            items.append(items_item)

        limit = d.pop("limit")

        next_cursor = d.pop("next_cursor", UNSET)

        data_repair_health = cls(
            observed_at=observed_at,
            phases=phases,
            held_slots=held_slots,
            held_memory_mb=held_memory_mb,
            held_vcpus=held_vcpus,
            held_cpu_millicores=held_cpu_millicores,
            unavailable_tasks=unavailable_tasks,
            items=items,
            limit=limit,
            next_cursor=next_cursor,
        )

        data_repair_health.additional_properties = d
        return data_repair_health

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
