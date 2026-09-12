from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.job_task_log_response_task_status import (
    JobTaskLogResponseTaskStatus,
    check_job_task_log_response_task_status,
)

T = TypeVar("T", bound="JobTaskLogResponse")


@_attrs_define
class JobTaskLogResponse:
    """Durable per-task combined stdout/stderr tail. Truncated=true
    means the tail was capped at MaxBytes; clients re-fetch
    with a larger limit to see more.

    """

    task_status: JobTaskLogResponseTaskStatus
    log_content: str
    truncated: bool
    max_bytes: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        task_status: str = self.task_status

        log_content = self.log_content

        truncated = self.truncated

        max_bytes = self.max_bytes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "task_status": task_status,
                "log_content": log_content,
                "truncated": truncated,
                "max_bytes": max_bytes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        task_status = check_job_task_log_response_task_status(d.pop("task_status"))

        log_content = d.pop("log_content")

        truncated = d.pop("truncated")

        max_bytes = d.pop("max_bytes")

        job_task_log_response = cls(
            task_status=task_status,
            log_content=log_content,
            truncated=truncated,
            max_bytes=max_bytes,
        )

        job_task_log_response.additional_properties = d
        return job_task_log_response

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
