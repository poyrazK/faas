from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateAgentExecutionFileGrantRequest")


@_attrs_define
class CreateAgentExecutionFileGrantRequest:
    """Scoped Agent endpoint, thread, call, task version and input path for staging a file."""

    endpoint_id: UUID
    thread_id: str
    tool_call_id: str
    task_name: str
    version: int
    path: str
    """Normalized relative path in the guest input directory."""
    step_label: str | Unset = UNSET
    """Exact pipeline step label. Omit for a standalone Agent task call."""

    def to_dict(self) -> dict[str, Any]:
        endpoint_id = str(self.endpoint_id)

        thread_id = self.thread_id

        tool_call_id = self.tool_call_id

        task_name = self.task_name

        version = self.version

        path = self.path

        step_label = self.step_label

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "endpoint_id": endpoint_id,
                "thread_id": thread_id,
                "tool_call_id": tool_call_id,
                "task_name": task_name,
                "version": version,
                "path": path,
            }
        )
        if step_label is not UNSET:
            field_dict["step_label"] = step_label

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        endpoint_id = UUID(d.pop("endpoint_id"))

        thread_id = d.pop("thread_id")

        tool_call_id = d.pop("tool_call_id")

        task_name = d.pop("task_name")

        version = d.pop("version")

        path = d.pop("path")

        step_label = d.pop("step_label", UNSET)

        create_agent_execution_file_grant_request = cls(
            endpoint_id=endpoint_id,
            thread_id=thread_id,
            tool_call_id=tool_call_id,
            task_name=task_name,
            version=version,
            path=path,
            step_label=step_label,
        )

        return create_agent_execution_file_grant_request
