from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.task_tool_quarantine_request import TaskToolQuarantineRequest
    from ..models.task_tool_release_execution_request import TaskToolReleaseExecutionRequest


T = TypeVar("T", bound="TaskToolReleaseIntentCheck")


@_attrs_define
class TaskToolReleaseIntentCheck:
    """Fresh local fingerprint for drift detection or a separately authorized adapter claim transition."""

    release_fingerprint: str
    quarantine: TaskToolQuarantineRequest | Unset = UNSET
    """Private trusted CLI qualification or attempt metadata on existing journal routes. Agent tools accept public
    identities only. A claim is never automatically retried; observe records read-only fence evidence after an
    admitted attempt."""
    execution: TaskToolReleaseExecutionRequest | Unset = UNSET
    """Exact local execution identity and bounded transition. Private claim material is submitted by the trusted
    CLI and is never returned in API receipts."""

    def to_dict(self) -> dict[str, Any]:
        release_fingerprint = self.release_fingerprint

        quarantine: dict[str, Any] | Unset = UNSET
        if not isinstance(self.quarantine, Unset):
            quarantine = self.quarantine.to_dict()

        execution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.execution, Unset):
            execution = self.execution.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "release_fingerprint": release_fingerprint,
            }
        )
        if quarantine is not UNSET:
            field_dict["quarantine"] = quarantine
        if execution is not UNSET:
            field_dict["execution"] = execution

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.task_tool_quarantine_request import TaskToolQuarantineRequest
        from ..models.task_tool_release_execution_request import TaskToolReleaseExecutionRequest

        d = dict(src_dict)
        release_fingerprint = d.pop("release_fingerprint")

        _quarantine = d.pop("quarantine", UNSET)
        quarantine: TaskToolQuarantineRequest | Unset
        if isinstance(_quarantine, Unset):
            quarantine = UNSET
        else:
            quarantine = TaskToolQuarantineRequest.from_dict(_quarantine)

        _execution = d.pop("execution", UNSET)
        execution: TaskToolReleaseExecutionRequest | Unset
        if isinstance(_execution, Unset):
            execution = UNSET
        else:
            execution = TaskToolReleaseExecutionRequest.from_dict(_execution)

        task_tool_release_intent_check = cls(
            release_fingerprint=release_fingerprint,
            quarantine=quarantine,
            execution=execution,
        )

        return task_tool_release_intent_check
