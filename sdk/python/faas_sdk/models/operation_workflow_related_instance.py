from __future__ import annotations
from collections.abc import Mapping
from typing import Any, TypeVar
from attrs import define
from ..types import UNSET, Unset
from .operation_workflow_dependency import OperationWorkflowDependency
from .operation_workflow_state import OperationWorkflowState
T=TypeVar("T",bound="OperationWorkflowRelatedInstance")
@define
class OperationWorkflowRelatedInstance:
    dependency: OperationWorkflowDependency
    status: str
    state: OperationWorkflowState | Unset = UNSET
    def to_dict(self) -> dict[str, Any]:
        result={"dependency":self.dependency.to_dict(),"status":self.status}
        if self.state is not UNSET: result["state"]=self.state.to_dict()
        return result
    @classmethod
    def from_dict(cls: type[T],src_dict: Mapping[str, Any]) -> T:
        return cls(dependency=OperationWorkflowDependency.from_dict(src_dict["dependency"]),status=src_dict["status"],state=OperationWorkflowState.from_dict(src_dict["state"]) if src_dict.get("state") is not None else UNSET)
