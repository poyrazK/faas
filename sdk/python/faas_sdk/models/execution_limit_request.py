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
from ..models.execution_limit_request_memory_mb import (
    ExecutionLimitRequestMemoryMb,
    check_execution_limit_request_memory_mb,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ExecutionLimitRequest")


@_attrs_define
class ExecutionLimitRequest:
    """Caller-selected execution resource limits; zero selects the plan default."""

    timeout_ms: int | Unset = UNSET
    """Zero uses the plan default."""
    memory_mb: ExecutionLimitRequestMemoryMb | Unset = UNSET
    cpu_millicores: ExecutionLimitRequestCpuMillicores | Unset = UNSET
    ephemeral_disk_mb: ExecutionLimitRequestEphemeralDiskMb | Unset = UNSET
    max_output_bytes: int | Unset = UNSET
    """Combined result/stdout/stderr cap; zero uses the plan default."""

    def to_dict(self) -> dict[str, Any]:
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
            timeout_ms=timeout_ms,
            memory_mb=memory_mb,
            cpu_millicores=cpu_millicores,
            ephemeral_disk_mb=ephemeral_disk_mb,
            max_output_bytes=max_output_bytes,
        )

        return execution_limit_request
