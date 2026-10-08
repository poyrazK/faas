from __future__ import annotations

from collections.abc import Mapping
from typing import Any, Literal, TypeVar, cast
from attrs import define
from ..types import UNSET, Unset
from .operation_workflow_blocker import OperationWorkflowBlocker
from .operation_workflow_instance_transition import OperationWorkflowInstanceTransition

T = TypeVar("T", bound="OperationWorkflowDecision")

@define
class OperationWorkflowDecision:
    """Contract options from reported state; not execution authorization."""
    reason: Literal["state_unknown", "terminal", "no_declared_transition", "transitions_available", "state_stale", "application_blocked", "deadline_overdue", "dependency_waiting"]
    explanation: str
    needs_attention: bool
    next_actions: list[OperationWorkflowInstanceTransition]
    state_revision: int | Unset = UNSET

    blockers: list[OperationWorkflowBlocker] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        result: dict[str, Any] = {"reason": self.reason, "explanation": self.explanation,
            "needs_attention": self.needs_attention, "next_actions": [a.to_dict() for a in self.next_actions]}
        if not isinstance(self.state_revision, Unset):
            result["state_revision"] = self.state_revision
        if not isinstance(self.blockers, Unset):
            result["blockers"] = [b.to_dict() for b in self.blockers]
        return result

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        return cls(blockers=[OperationWorkflowBlocker.from_dict(b) for b in src_dict["blockers"]] if "blockers" in src_dict else UNSET, reason=cast(Any, src_dict["reason"]), explanation=src_dict["explanation"],
            needs_attention=src_dict["needs_attention"],
            next_actions=[OperationWorkflowInstanceTransition.from_dict(a) for a in src_dict["next_actions"]],
            state_revision=src_dict.get("state_revision", UNSET))
