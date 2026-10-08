from __future__ import annotations
from collections.abc import Mapping
from typing import Any, TypeVar
from attrs import define
from ..types import UNSET, Unset
from uuid import UUID
from .operation_subject import OperationSubject
from .operation_workflow_planned_effect import OperationWorkflowPlannedEffect
from .operation_workflow_planned_invariant import OperationWorkflowPlannedInvariant
from .operation_workflow_planned_decision import OperationWorkflowPlannedDecision
T=TypeVar("T",bound="OperationWorkflowReadinessRequest")
@define
class OperationWorkflowReadinessRequest:
    scope: str
    subject: OperationSubject
    workflow: str
    instance_id: str
    operation: str
    from_state: str
    to_state: str
    app_id: UUID | Unset = UNSET
    tenant_id: UUID | Unset = UNSET
    effects: list[OperationWorkflowPlannedEffect] | Unset = UNSET
    invariants: list[OperationWorkflowPlannedInvariant] | Unset = UNSET
    decisions: list[OperationWorkflowPlannedDecision] | Unset = UNSET
    milestones: list[str] | Unset = UNSET
    state_revision: int | Unset = UNSET
    contract_version: int | Unset = UNSET
    def to_dict(self) -> dict[str, Any]:
        result={key:getattr(self,key) for key in ("scope","workflow","instance_id","operation","from_state","to_state")}
        result["subject"]=self.subject.to_dict()
        for key in ("app_id","tenant_id","milestones","state_revision","contract_version"):
            value=getattr(self,key)
            if value is not UNSET: result[key]=str(value) if key.endswith("_id") else value
        if self.decisions is not UNSET: result["decisions"]=[v.to_dict() for v in self.decisions]
        if self.invariants is not UNSET: result["invariants"]=[v.to_dict() for v in self.invariants]
        if self.effects is not UNSET: result["effects"]=[v.to_dict() for v in self.effects]
        return result
    @classmethod
    def from_dict(cls:type[T],src_dict:Mapping[str,Any])->T:
        data={key:src_dict[key] for key in ("scope","workflow","instance_id","operation","from_state","to_state")}
        data["subject"]=OperationSubject.from_dict(src_dict["subject"])
        for key in ("app_id","tenant_id","milestones","state_revision","contract_version"):
            if key in src_dict: data[key]=UUID(src_dict[key]) if key.endswith("_id") else src_dict[key]
        if "decisions" in src_dict: data["decisions"]=[OperationWorkflowPlannedDecision.from_dict(v) for v in src_dict["decisions"]]
        if "invariants" in src_dict: data["invariants"]=[OperationWorkflowPlannedInvariant.from_dict(v) for v in src_dict["invariants"]]
        if "effects" in src_dict: data["effects"]=[OperationWorkflowPlannedEffect.from_dict(v) for v in src_dict["effects"]]
        return cls(**data)
