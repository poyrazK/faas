from __future__ import annotations
from collections.abc import Mapping
from typing import Any, TypeVar
from attrs import define
from ..types import UNSET, Unset
from .operation_workflow_policy_requirement import OperationWorkflowPolicyRequirement
from .operation_workflow_unmet_invariant import OperationWorkflowUnmetInvariant
from .operation_workflow_unmet_effect import OperationWorkflowUnmetEffect
from .operation_workflow_instance_transition import OperationWorkflowInstanceTransition
from .operation_workflow_blocker import OperationWorkflowBlocker
from .operation_workflow_related_instance import OperationWorkflowRelatedInstance
T=TypeVar("T",bound="OperationWorkflowTransitionReadiness")
@define
class OperationWorkflowTransitionReadiness:
    transition: OperationWorkflowInstanceTransition
    declared: bool
    ready: bool
    reasons: list[str]
    advisories: list[str]
    contract_version: int
    blockers: list[OperationWorkflowBlocker]
    unmet_dependencies: list[OperationWorkflowRelatedInstance]
    missing_milestones: list[str]
    unmet_effects: list[OperationWorkflowUnmetEffect] | Unset = UNSET
    unmet_invariants: list[OperationWorkflowUnmetInvariant] | Unset = UNSET
    invariant_blockers: list[OperationWorkflowBlocker] | Unset = UNSET
    missing_dependency_workflows: list[str] | Unset = UNSET
    missing_policies: list[OperationWorkflowPolicyRequirement] | Unset = UNSET
    state_revision: int | Unset = UNSET
    def to_dict(self)->dict[str,Any]:
        result={key:getattr(self,key) for key in ("declared","ready","reasons","advisories","contract_version","missing_milestones")}
        result["transition"]=self.transition.to_dict()
        result["blockers"]=[v.to_dict() for v in self.blockers]
        result["unmet_dependencies"]=[v.to_dict() for v in self.unmet_dependencies]
        if self.state_revision is not UNSET: result["state_revision"]=self.state_revision
        if self.missing_policies is not UNSET: result["missing_policies"]=[v.to_dict() for v in self.missing_policies]
        if self.missing_dependency_workflows is not UNSET: result["missing_dependency_workflows"]=self.missing_dependency_workflows
        if self.invariant_blockers is not UNSET: result["invariant_blockers"]=[v.to_dict() for v in self.invariant_blockers]
        if self.unmet_invariants is not UNSET: result["unmet_invariants"]=[v.to_dict() for v in self.unmet_invariants]
        if self.unmet_effects is not UNSET: result["unmet_effects"]=[v.to_dict() for v in self.unmet_effects]
        return result
    @classmethod
    def from_dict(cls:type[T],src_dict:Mapping[str,Any])->T:
        data={key:src_dict[key] for key in ("declared","ready","reasons","advisories","contract_version","missing_milestones")}
        return cls(**data,unmet_effects=[OperationWorkflowUnmetEffect.from_dict(v) for v in src_dict["unmet_effects"]] if "unmet_effects" in src_dict else UNSET,unmet_invariants=[OperationWorkflowUnmetInvariant.from_dict(v) for v in src_dict["unmet_invariants"]] if "unmet_invariants" in src_dict else UNSET,invariant_blockers=[OperationWorkflowBlocker.from_dict(v) for v in src_dict["invariant_blockers"]] if "invariant_blockers" in src_dict else UNSET,missing_dependency_workflows=src_dict.get("missing_dependency_workflows",UNSET),missing_policies=[OperationWorkflowPolicyRequirement.from_dict(v) for v in src_dict["missing_policies"]] if "missing_policies" in src_dict else UNSET,transition=OperationWorkflowInstanceTransition.from_dict(src_dict["transition"]),blockers=[OperationWorkflowBlocker.from_dict(v) for v in src_dict["blockers"]],unmet_dependencies=[OperationWorkflowRelatedInstance.from_dict(v) for v in src_dict["unmet_dependencies"]],state_revision=src_dict.get("state_revision",UNSET))
