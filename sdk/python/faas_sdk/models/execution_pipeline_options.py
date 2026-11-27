from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ExecutionPipelineOptions")


@_attrs_define
class ExecutionPipelineOptions:
    """Preview; v2 only. Enables durable task steps, batch expansion and explicit retries. Submit exactly one of steps or
    batch. Requires FAAS_EXECUTION_PIPELINES_ENABLED plus v2 and tasks gates. Queueing and backoff count toward the
    deadline.

    """

    total_timeout_ms: int | Unset = 3600000

    def to_dict(self) -> dict[str, Any]:
        total_timeout_ms = self.total_timeout_ms

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if total_timeout_ms is not UNSET:
            field_dict["total_timeout_ms"] = total_timeout_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        total_timeout_ms = d.pop("total_timeout_ms", UNSET)

        execution_pipeline_options = cls(
            total_timeout_ms=total_timeout_ms,
        )

        return execution_pipeline_options
