from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_effect_requirement import OperationWorkflowEffectRequirement
    from ..models.operation_workflow_invariant_requirement import OperationWorkflowInvariantRequirement
    from ..models.operation_workflow_policy_requirement import OperationWorkflowPolicyRequirement


T = TypeVar("T", bound="OperationWorkflowInstanceTransition")


@_attrs_define
class OperationWorkflowInstanceTransition:
    """One allowed state edge from the selected contract version, bound to the Operation that can report it. Required
    milestones must be committed with the transition in the same application transaction. This declaration does not
    establish that the edge is currently valid for the application's business row or authorized for the caller.

    """

    from_: str
    to: str
    operation: str
    """Manifest operation name that may report this transition."""
    required_effects: list[OperationWorkflowEffectRequirement] | Unset = UNSET
    required_invariants: list[OperationWorkflowInvariantRequirement] | Unset = UNSET
    required_dependency_workflows: list[str] | Unset = UNSET
    """Omitted means all reported dependencies; an empty array means none. Named workflows require at least one
    reported link and every matching link must meet its outcome requirement."""
    required_policies: list[OperationWorkflowPolicyRequirement] | Unset = UNSET
    required_milestones: list[str] | Unset = UNSET
    """Milestone names that must be committed in the same application transaction as this transition."""

    def to_dict(self) -> dict[str, Any]:
        from_ = self.from_

        to = self.to

        operation = self.operation

        required_effects: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.required_effects, Unset):
            required_effects = []
            for required_effects_item_data in self.required_effects:
                required_effects_item = required_effects_item_data.to_dict()
                required_effects.append(required_effects_item)

        required_invariants: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.required_invariants, Unset):
            required_invariants = []
            for required_invariants_item_data in self.required_invariants:
                required_invariants_item = required_invariants_item_data.to_dict()
                required_invariants.append(required_invariants_item)

        required_dependency_workflows: list[str] | Unset = UNSET
        if not isinstance(self.required_dependency_workflows, Unset):
            required_dependency_workflows = self.required_dependency_workflows

        required_policies: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.required_policies, Unset):
            required_policies = []
            for required_policies_item_data in self.required_policies:
                required_policies_item = required_policies_item_data.to_dict()
                required_policies.append(required_policies_item)

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
        if required_effects is not UNSET:
            field_dict["required_effects"] = required_effects
        if required_invariants is not UNSET:
            field_dict["required_invariants"] = required_invariants
        if required_dependency_workflows is not UNSET:
            field_dict["required_dependency_workflows"] = required_dependency_workflows
        if required_policies is not UNSET:
            field_dict["required_policies"] = required_policies
        if required_milestones is not UNSET:
            field_dict["required_milestones"] = required_milestones

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_effect_requirement import OperationWorkflowEffectRequirement
        from ..models.operation_workflow_invariant_requirement import OperationWorkflowInvariantRequirement
        from ..models.operation_workflow_policy_requirement import OperationWorkflowPolicyRequirement

        d = dict(src_dict)
        from_ = d.pop("from")

        to = d.pop("to")

        operation = d.pop("operation")

        _required_effects = d.pop("required_effects", UNSET)
        required_effects: list[OperationWorkflowEffectRequirement] | Unset = UNSET
        if _required_effects is not UNSET:
            required_effects = []
            for required_effects_item_data in _required_effects:
                required_effects_item = OperationWorkflowEffectRequirement.from_dict(required_effects_item_data)

                required_effects.append(required_effects_item)

        _required_invariants = d.pop("required_invariants", UNSET)
        required_invariants: list[OperationWorkflowInvariantRequirement] | Unset = UNSET
        if _required_invariants is not UNSET:
            required_invariants = []
            for required_invariants_item_data in _required_invariants:
                required_invariants_item = OperationWorkflowInvariantRequirement.from_dict(
                    required_invariants_item_data
                )

                required_invariants.append(required_invariants_item)

        required_dependency_workflows = cast(list[str], d.pop("required_dependency_workflows", UNSET))

        _required_policies = d.pop("required_policies", UNSET)
        required_policies: list[OperationWorkflowPolicyRequirement] | Unset = UNSET
        if _required_policies is not UNSET:
            required_policies = []
            for required_policies_item_data in _required_policies:
                required_policies_item = OperationWorkflowPolicyRequirement.from_dict(required_policies_item_data)

                required_policies.append(required_policies_item)

        required_milestones = cast(list[str], d.pop("required_milestones", UNSET))

        operation_workflow_instance_transition = cls(
            from_=from_,
            to=to,
            operation=operation,
            required_effects=required_effects,
            required_invariants=required_invariants,
            required_dependency_workflows=required_dependency_workflows,
            required_policies=required_policies,
            required_milestones=required_milestones,
        )

        return operation_workflow_instance_transition
