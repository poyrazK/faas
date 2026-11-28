from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.execution_limit_request_cpu_millicores import (
    ExecutionLimitRequestCpuMillicores,
    check_execution_limit_request_cpu_millicores,
)
from ..models.execution_limit_request_ephemeral_disk_mb import (
    ExecutionLimitRequestEphemeralDiskMb,
    check_execution_limit_request_ephemeral_disk_mb,
)
from ..models.execution_limit_request_execution_class import (
    ExecutionLimitRequestExecutionClass,
    check_execution_limit_request_execution_class,
)
from ..models.execution_limit_request_memory_mb import (
    ExecutionLimitRequestMemoryMb,
    check_execution_limit_request_memory_mb,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ExecutionLimitRequest")


@_attrs_define
class ExecutionLimitRequest:
    """Caller-selected execution resource limits; zero selects the plan default."""

    execution_class: ExecutionLimitRequestExecutionClass | Unset = UNSET
    """Standard preserves existing plan limits; extended opts into an admission-to-result budget up to 300000 ms
    and a smaller shared concurrency cap."""
    max_artifact_bytes: int | Unset = UNSET
    """Independent stored output byte budget; requires stored output_files. Zero selects the plan default
    constrained by scratch headroom."""
    timeout_ms: int | Unset = UNSET
    """Zero uses the class default: standard 5000 ms, extended 300000 ms. Standard plan maxima remain unchanged."""
    memory_mb: ExecutionLimitRequestMemoryMb | Unset = UNSET
    cpu_millicores: ExecutionLimitRequestCpuMillicores | Unset = UNSET
    ephemeral_disk_mb: ExecutionLimitRequestEphemeralDiskMb | Unset = UNSET
    max_output_bytes: int | Unset = UNSET
    """Combined result/stdout/stderr/serialized-artifacts cap; zero uses the plan default."""

    def to_dict(self) -> dict[str, Any]:
        execution_class: str | Unset = UNSET
        if not isinstance(self.execution_class, Unset):
            execution_class = self.execution_class

        max_artifact_bytes = self.max_artifact_bytes

        timeout_ms = self.timeout_ms

        memory_mb: int | Unset = UNSET
        if not isinstance(self.memory_mb, Unset):
            memory_mb = self.memory_mb

        cpu_millicores: int | Unset = UNSET
        if not isinstance(self.cpu_millicores, Unset):
            cpu_millicores = self.cpu_millicores

        ephemeral_disk_mb: int | Unset = UNSET
        if not isinstance(self.ephemeral_disk_mb, Unset):
            ephemeral_disk_mb = self.ephemeral_disk_mb

        max_output_bytes = self.max_output_bytes

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if execution_class is not UNSET:
            field_dict["execution_class"] = execution_class
        if max_artifact_bytes is not UNSET:
            field_dict["max_artifact_bytes"] = max_artifact_bytes
        if timeout_ms is not UNSET:
            field_dict["timeout_ms"] = timeout_ms
        if memory_mb is not UNSET:
            field_dict["memory_mb"] = memory_mb
        if cpu_millicores is not UNSET:
            field_dict["cpu_millicores"] = cpu_millicores
        if ephemeral_disk_mb is not UNSET:
            field_dict["ephemeral_disk_mb"] = ephemeral_disk_mb
        if max_output_bytes is not UNSET:
            field_dict["max_output_bytes"] = max_output_bytes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _execution_class = d.pop("execution_class", UNSET)
        execution_class: ExecutionLimitRequestExecutionClass | Unset
        if isinstance(_execution_class, Unset):
            execution_class = UNSET
        else:
            execution_class = check_execution_limit_request_execution_class(_execution_class)

        max_artifact_bytes = d.pop("max_artifact_bytes", UNSET)

        timeout_ms = d.pop("timeout_ms", UNSET)

        _memory_mb = d.pop("memory_mb", UNSET)
        memory_mb: ExecutionLimitRequestMemoryMb | Unset
        if isinstance(_memory_mb, Unset):
            memory_mb = UNSET
        else:
            memory_mb = check_execution_limit_request_memory_mb(_memory_mb)

        _cpu_millicores = d.pop("cpu_millicores", UNSET)
        cpu_millicores: ExecutionLimitRequestCpuMillicores | Unset
        if isinstance(_cpu_millicores, Unset):
            cpu_millicores = UNSET
        else:
            cpu_millicores = check_execution_limit_request_cpu_millicores(_cpu_millicores)

        _ephemeral_disk_mb = d.pop("ephemeral_disk_mb", UNSET)
        ephemeral_disk_mb: ExecutionLimitRequestEphemeralDiskMb | Unset
        if isinstance(_ephemeral_disk_mb, Unset):
            ephemeral_disk_mb = UNSET
        else:
            ephemeral_disk_mb = check_execution_limit_request_ephemeral_disk_mb(_ephemeral_disk_mb)

        max_output_bytes = d.pop("max_output_bytes", UNSET)

        execution_limit_request = cls(
            execution_class=execution_class,
            max_artifact_bytes=max_artifact_bytes,
            timeout_ms=timeout_ms,
            memory_mb=memory_mb,
            cpu_millicores=cpu_millicores,
            ephemeral_disk_mb=ephemeral_disk_mb,
            max_output_bytes=max_output_bytes,
        )

        return execution_limit_request
