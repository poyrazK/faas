from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.task_tool_release_intent_decision_decision import (
    TaskToolReleaseIntentDecisionDecision,
    check_task_tool_release_intent_decision_decision,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.task_tool_release_execution_request import TaskToolReleaseExecutionRequest


T = TypeVar("T", bound="TaskToolReleaseIntentDecision")


@_attrs_define
class TaskToolReleaseIntentDecision:
    """Owner verdict bound to an immutable Task intent digest and its retained source fingerprint."""

    intent_sha256: str
    release_fingerprint: str
    decision: TaskToolReleaseIntentDecisionDecision
    execution: TaskToolReleaseExecutionRequest | Unset = UNSET
    """Exact local execution identity and bounded transition. Private claim material is submitted by the trusted
    CLI and is never returned in API receipts."""

    def to_dict(self) -> dict[str, Any]:
        intent_sha256 = self.intent_sha256

        release_fingerprint = self.release_fingerprint

        decision: str = self.decision

        execution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.execution, Unset):
            execution = self.execution.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "intent_sha256": intent_sha256,
                "release_fingerprint": release_fingerprint,
                "decision": decision,
            }
        )
        if execution is not UNSET:
            field_dict["execution"] = execution

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.task_tool_release_execution_request import TaskToolReleaseExecutionRequest

        d = dict(src_dict)
        intent_sha256 = d.pop("intent_sha256")

        release_fingerprint = d.pop("release_fingerprint")

        decision = check_task_tool_release_intent_decision_decision(d.pop("decision"))

        _execution = d.pop("execution", UNSET)
        execution: TaskToolReleaseExecutionRequest | Unset
        if isinstance(_execution, Unset):
            execution = UNSET
        else:
            execution = TaskToolReleaseExecutionRequest.from_dict(_execution)

        task_tool_release_intent_decision = cls(
            intent_sha256=intent_sha256,
            release_fingerprint=release_fingerprint,
            decision=decision,
            execution=execution,
        )

        return task_tool_release_intent_decision
