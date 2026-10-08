from __future__ import annotations
from collections.abc import Mapping
from typing import Any, TypeVar
from attrs import define
from ..types import UNSET, Unset
from .operation_workflow_transition_readiness import OperationWorkflowTransitionReadiness
T=TypeVar("T",bound="OperationWorkflowReadinessOverview")
@define
class OperationWorkflowReadinessOverview:
    items: list[OperationWorkflowTransitionReadiness]
    transition_count: int
    has_more: bool
    def to_dict(self)->dict[str,Any]:
        return {"items":[v.to_dict() for v in self.items],"transition_count":self.transition_count,"has_more":self.has_more}
    @classmethod
    def from_dict(cls:type[T],src_dict:Mapping[str,Any])->T:
        return cls(items=[OperationWorkflowTransitionReadiness.from_dict(v) for v in src_dict["items"]],transition_count=src_dict["transition_count"],has_more=src_dict["has_more"])
