from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ExecutionCapabilityLimits")


@_attrs_define
class ExecutionCapabilityLimits:
    """Plan limits and fixed request-shape caps for Runs."""

    max_concurrent_runs: int
    max_source_bytes: int
    max_input_bytes: int
    default_output_bytes: int
    max_output_bytes: int
    default_timeout_ms: int
    max_timeout_ms: int
    default_memory_mb: int
    max_memory_mb: int
    default_cpu_millicores: int
    max_cpu_millicores: int
    default_ephemeral_disk_mb: int
    max_ephemeral_disk_mb: int
    pids_max: int
    max_bundle_files: int
    max_artifact_inputs: int
    max_output_files: int
    max_artifact_path_bytes: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        max_concurrent_runs = self.max_concurrent_runs

        max_source_bytes = self.max_source_bytes

        max_input_bytes = self.max_input_bytes

        default_output_bytes = self.default_output_bytes

        max_output_bytes = self.max_output_bytes

        default_timeout_ms = self.default_timeout_ms

        max_timeout_ms = self.max_timeout_ms

        default_memory_mb = self.default_memory_mb

        max_memory_mb = self.max_memory_mb

        default_cpu_millicores = self.default_cpu_millicores

        max_cpu_millicores = self.max_cpu_millicores

        default_ephemeral_disk_mb = self.default_ephemeral_disk_mb

        max_ephemeral_disk_mb = self.max_ephemeral_disk_mb

        pids_max = self.pids_max

        max_bundle_files = self.max_bundle_files

        max_artifact_inputs = self.max_artifact_inputs

        max_output_files = self.max_output_files

        max_artifact_path_bytes = self.max_artifact_path_bytes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "max_concurrent_runs": max_concurrent_runs,
                "max_source_bytes": max_source_bytes,
                "max_input_bytes": max_input_bytes,
                "default_output_bytes": default_output_bytes,
                "max_output_bytes": max_output_bytes,
                "default_timeout_ms": default_timeout_ms,
                "max_timeout_ms": max_timeout_ms,
                "default_memory_mb": default_memory_mb,
                "max_memory_mb": max_memory_mb,
                "default_cpu_millicores": default_cpu_millicores,
                "max_cpu_millicores": max_cpu_millicores,
                "default_ephemeral_disk_mb": default_ephemeral_disk_mb,
                "max_ephemeral_disk_mb": max_ephemeral_disk_mb,
                "pids_max": pids_max,
                "max_bundle_files": max_bundle_files,
                "max_artifact_inputs": max_artifact_inputs,
                "max_output_files": max_output_files,
                "max_artifact_path_bytes": max_artifact_path_bytes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_concurrent_runs = d.pop("max_concurrent_runs")

        max_source_bytes = d.pop("max_source_bytes")

        max_input_bytes = d.pop("max_input_bytes")

        default_output_bytes = d.pop("default_output_bytes")

        max_output_bytes = d.pop("max_output_bytes")

        default_timeout_ms = d.pop("default_timeout_ms")

        max_timeout_ms = d.pop("max_timeout_ms")

        default_memory_mb = d.pop("default_memory_mb")

        max_memory_mb = d.pop("max_memory_mb")

        default_cpu_millicores = d.pop("default_cpu_millicores")

        max_cpu_millicores = d.pop("max_cpu_millicores")

        default_ephemeral_disk_mb = d.pop("default_ephemeral_disk_mb")

        max_ephemeral_disk_mb = d.pop("max_ephemeral_disk_mb")

        pids_max = d.pop("pids_max")

        max_bundle_files = d.pop("max_bundle_files")

        max_artifact_inputs = d.pop("max_artifact_inputs")

        max_output_files = d.pop("max_output_files")

        max_artifact_path_bytes = d.pop("max_artifact_path_bytes")

        execution_capability_limits = cls(
            max_concurrent_runs=max_concurrent_runs,
            max_source_bytes=max_source_bytes,
            max_input_bytes=max_input_bytes,
            default_output_bytes=default_output_bytes,
            max_output_bytes=max_output_bytes,
            default_timeout_ms=default_timeout_ms,
            max_timeout_ms=max_timeout_ms,
            default_memory_mb=default_memory_mb,
            max_memory_mb=max_memory_mb,
            default_cpu_millicores=default_cpu_millicores,
            max_cpu_millicores=max_cpu_millicores,
            default_ephemeral_disk_mb=default_ephemeral_disk_mb,
            max_ephemeral_disk_mb=max_ephemeral_disk_mb,
            pids_max=pids_max,
            max_bundle_files=max_bundle_files,
            max_artifact_inputs=max_artifact_inputs,
            max_output_files=max_output_files,
            max_artifact_path_bytes=max_artifact_path_bytes,
        )

        execution_capability_limits.additional_properties = d
        return execution_capability_limits

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
