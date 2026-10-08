from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset
from .operation_workflow_effect_requirement import OperationWorkflowEffectRequirement
from .operation_workflow_invariant_requirement import OperationWorkflowInvariantRequirement
from .operation_workflow_policy_requirement import OperationWorkflowPolicyRequirement

T = TypeVar("T", bound="OperationWorkflowInstanceTransition")


@_attrs_define
class OperationWorkflowInstanceTransition:
    """Allowed edge from the selected workflow contract, bound to its target Operation."""

    from_: str
    to: str
    operation: str
    required_dependency_workflows: list[str] | Unset = UNSET
    required_effects: list[OperationWorkflowEffectRequirement] | Unset = UNSET
    required_invariants: list[OperationWorkflowInvariantRequirement] | Unset = UNSET
    required_policies: list[OperationWorkflowPolicyRequirement] | Unset = UNSET
    required_milestones: list[str] | Unset = UNSET
    """Milestones that must be committed with the transition in the same application transaction."""

    def to_dict(self) -> dict[str, Any]:
        from_ = self.from_

        to = self.to

        operation = self.operation

        required_milestones: list[str] | Unset = UNSET
        if not isinstance(self.required_milestones, Unset):
            required_milestones = self.required_milestones

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "from": from_,
                "to": to,
                "operation": operation,
            }
        )
        if required_milestones is not UNSET:
            field_dict["required_milestones"] = required_milestones

        if self.required_policies is not UNSET: field_dict["required_policies"]=[v.to_dict() for v in self.required_policies]
        if self.required_dependency_workflows is not UNSET: field_dict["required_dependency_workflows"]=self.required_dependency_workflows
        if self.required_invariants is not UNSET: field_dict["required_invariants"]=[v.to_dict() for v in self.required_invariants]
        if self.required_effects is not UNSET: field_dict["required_effects"]=[v.to_dict() for v in self.required_effects]
        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        from_ = d.pop("from")

        to = d.pop("to")

        operation = d.pop("operation")

        required_milestones = cast(list[str], d.pop("required_milestones", UNSET))

        operation_workflow_instance_transition = cls(
            from_=from_,
            to=to,
            operation=operation,
            required_milestones=required_milestones,
            required_effects=[OperationWorkflowEffectRequirement.from_dict(v) for v in d["required_effects"]] if "required_effects" in d else UNSET,
            required_invariants=[OperationWorkflowInvariantRequirement.from_dict(v) for v in d["required_invariants"]] if "required_invariants" in d else UNSET,
            required_dependency_workflows=d.get("required_dependency_workflows",UNSET),
            required_policies=[OperationWorkflowPolicyRequirement.from_dict(v) for v in d["required_policies"]] if "required_policies" in d else UNSET,
        )

        return operation_workflow_instance_transition
