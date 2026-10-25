from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugTelemetryListFilters")


@_attrs_define
class DebugTelemetryListFilters:
    """Normalized server-side filters echoed by a request telemetry page."""

    deployment_id: UUID | Unset = UNSET
    status: int | Unset = UNSET
    cold_boot: bool | Unset = UNSET
    consumer_id: str | Unset = UNSET
    """Consumer UUID, or __anonymous__ for anonymous traffic."""
    min_latency_ms: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id: str | Unset = UNSET
        if not isinstance(self.deployment_id, Unset):
            deployment_id = str(self.deployment_id)

        status = self.status

        cold_boot = self.cold_boot

        consumer_id = self.consumer_id

        min_latency_ms = self.min_latency_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if deployment_id is not UNSET:
            field_dict["deployment_id"] = deployment_id
        if status is not UNSET:
            field_dict["status"] = status
        if cold_boot is not UNSET:
            field_dict["cold_boot"] = cold_boot
        if consumer_id is not UNSET:
            field_dict["consumer_id"] = consumer_id
        if min_latency_ms is not UNSET:
            field_dict["min_latency_ms"] = min_latency_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _deployment_id = d.pop("deployment_id", UNSET)
        deployment_id: UUID | Unset
        if isinstance(_deployment_id, Unset):
            deployment_id = UNSET
        else:
            deployment_id = UUID(_deployment_id)

        status = d.pop("status", UNSET)

        cold_boot = d.pop("cold_boot", UNSET)

        consumer_id = d.pop("consumer_id", UNSET)

        min_latency_ms = d.pop("min_latency_ms", UNSET)

        debug_telemetry_list_filters = cls(
            deployment_id=deployment_id,
            status=status,
            cold_boot=cold_boot,
            consumer_id=consumer_id,
            min_latency_ms=min_latency_ms,
        )

        debug_telemetry_list_filters.additional_properties = d
        return debug_telemetry_list_filters

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
