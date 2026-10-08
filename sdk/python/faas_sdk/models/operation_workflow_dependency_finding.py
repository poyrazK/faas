from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from attrs import define
from ..types import UNSET, Unset
from .operation_workflow_dependency import OperationWorkflowDependency
from .operation_workflow_state import OperationWorkflowState

T = TypeVar("T", bound="OperationWorkflowDependencyFinding")

@define
class OperationWorkflowDependencyFinding:
    kind: str
    path: list[OperationWorkflowDependency]
    explanation: str
    state: OperationWorkflowState | Unset = UNSET
    limit: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        result = {"kind": self.kind, "path": [v.to_dict() for v in self.path], "explanation": self.explanation}
        if self.state is not UNSET:
            result["state"] = self.state.to_dict()
        if self.limit is not UNSET:
            result["limit"] = self.limit
        return result

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        return cls(kind=src_dict["kind"], path=[OperationWorkflowDependency.from_dict(v) for v in src_dict["path"]],
                   explanation=src_dict["explanation"], limit=src_dict.get("limit", UNSET),
                   state=OperationWorkflowState.from_dict(src_dict["state"]) if src_dict.get("state") is not None else UNSET)
