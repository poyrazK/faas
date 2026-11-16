from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.task_tool_release_execution_request import TaskToolReleaseExecutionRequest


T = TypeVar("T", bound="TaskToolReleaseIntentCheck")


@_attrs_define
class TaskToolReleaseIntentCheck:
    """Fresh local fingerprint for drift detection or a separately authorized adapter claim transition."""

    release_fingerprint: str
    execution: TaskToolReleaseExecutionRequest | Unset = UNSET
    """Exact local execution identity and bounded transition. Private claim material is submitted by the trusted
    CLI and is never returned in API receipts."""

    def to_dict(self) -> dict[str, Any]:
        release_fingerprint = self.release_fingerprint

        execution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.execution, Unset):
            execution = self.execution.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "release_fingerprint": release_fingerprint,
            }
        )
        if execution is not UNSET:
            field_dict["execution"] = execution

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.task_tool_release_execution_request import TaskToolReleaseExecutionRequest

        d = dict(src_dict)
        release_fingerprint = d.pop("release_fingerprint")

        _execution = d.pop("execution", UNSET)
        execution: TaskToolReleaseExecutionRequest | Unset
        if isinstance(_execution, Unset):
            execution = UNSET
        else:
            execution = TaskToolReleaseExecutionRequest.from_dict(_execution)

        task_tool_release_intent_check = cls(
            release_fingerprint=release_fingerprint,
            execution=execution,
        )

        return task_tool_release_intent_check
