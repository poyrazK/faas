from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ExecutionUsage")


@_attrs_define
class ExecutionUsage:
    """Host-measured resource usage for a terminal execution."""

    wall_time_ms: int
    cpu_time_ms: int
    peak_memory_mb: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        wall_time_ms = self.wall_time_ms

        cpu_time_ms = self.cpu_time_ms

        peak_memory_mb = self.peak_memory_mb

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "wall_time_ms": wall_time_ms,
                "cpu_time_ms": cpu_time_ms,
                "peak_memory_mb": peak_memory_mb,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        wall_time_ms = d.pop("wall_time_ms")

        cpu_time_ms = d.pop("cpu_time_ms")

        peak_memory_mb = d.pop("peak_memory_mb")

        execution_usage = cls(
            wall_time_ms=wall_time_ms,
            cpu_time_ms=cpu_time_ms,
            peak_memory_mb=peak_memory_mb,
        )

        execution_usage.additional_properties = d
        return execution_usage

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
