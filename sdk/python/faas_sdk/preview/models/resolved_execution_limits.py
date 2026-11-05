from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ResolvedExecutionLimits")


@_attrs_define
class ResolvedExecutionLimits:
    """Immutable limits admitted and enforced for one execution."""

    timeout_ms: int
    memory_mb: int
    cpu_millicores: int
    ephemeral_disk_mb: int
    max_output_bytes: int
    pids_max: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        timeout_ms = self.timeout_ms

        memory_mb = self.memory_mb

        cpu_millicores = self.cpu_millicores

        ephemeral_disk_mb = self.ephemeral_disk_mb

        max_output_bytes = self.max_output_bytes

        pids_max = self.pids_max

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "timeout_ms": timeout_ms,
                "memory_mb": memory_mb,
                "cpu_millicores": cpu_millicores,
                "ephemeral_disk_mb": ephemeral_disk_mb,
                "max_output_bytes": max_output_bytes,
                "pids_max": pids_max,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        timeout_ms = d.pop("timeout_ms")

        memory_mb = d.pop("memory_mb")

        cpu_millicores = d.pop("cpu_millicores")

        ephemeral_disk_mb = d.pop("ephemeral_disk_mb")

        max_output_bytes = d.pop("max_output_bytes")

        pids_max = d.pop("pids_max")

        resolved_execution_limits = cls(
            timeout_ms=timeout_ms,
            memory_mb=memory_mb,
            cpu_millicores=cpu_millicores,
            ephemeral_disk_mb=ephemeral_disk_mb,
            max_output_bytes=max_output_bytes,
            pids_max=pids_max,
        )

        resolved_execution_limits.additional_properties = d
        return resolved_execution_limits

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
