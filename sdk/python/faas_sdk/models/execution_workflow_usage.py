from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ExecutionWorkflowUsage")


@_attrs_define
class ExecutionWorkflowUsage:
    """Host-measured usage summed over terminal runs; peak memory is the maximum individual run peak."""

    wall_time_ms: int
    cpu_time_ms: int
    peak_memory_mb: int
    output_bytes: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        wall_time_ms = self.wall_time_ms

        cpu_time_ms = self.cpu_time_ms

        peak_memory_mb = self.peak_memory_mb

        output_bytes = self.output_bytes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "wall_time_ms": wall_time_ms,
                "cpu_time_ms": cpu_time_ms,
                "peak_memory_mb": peak_memory_mb,
                "output_bytes": output_bytes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        wall_time_ms = d.pop("wall_time_ms")

        cpu_time_ms = d.pop("cpu_time_ms")

        peak_memory_mb = d.pop("peak_memory_mb")

        output_bytes = d.pop("output_bytes")

        execution_workflow_usage = cls(
            wall_time_ms=wall_time_ms,
            cpu_time_ms=cpu_time_ms,
            peak_memory_mb=peak_memory_mb,
            output_bytes=output_bytes,
        )

        execution_workflow_usage.additional_properties = d
        return execution_workflow_usage

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
