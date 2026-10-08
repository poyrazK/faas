from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from attrs import define
from ..types import UNSET, Unset
from .operation_subject import OperationSubject
from .operation_workflow_state import OperationWorkflowState

T = TypeVar("T", bound="OperationWorkflowDependentInstance")

@define
class OperationWorkflowDependentInstance:
    subject: OperationSubject
    state: OperationWorkflowState
    dependency_status: str
    needs_attention: bool
    required_outcome_code: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        result = {"subject": self.subject.to_dict(), "state": self.state.to_dict(),
                  "dependency_status": self.dependency_status, "needs_attention": self.needs_attention}
        if self.required_outcome_code is not UNSET:
            result["required_outcome_code"] = self.required_outcome_code
        return result

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        return cls(subject=OperationSubject.from_dict(src_dict["subject"]),
                   state=OperationWorkflowState.from_dict(src_dict["state"]),
                   dependency_status=src_dict["dependency_status"], needs_attention=src_dict["needs_attention"],
                   required_outcome_code=src_dict.get("required_outcome_code", UNSET))
