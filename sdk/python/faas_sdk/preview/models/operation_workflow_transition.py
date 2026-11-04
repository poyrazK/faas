from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_effect_requirement import OperationWorkflowEffectRequirement
    from ..models.operation_workflow_invariant_requirement import OperationWorkflowInvariantRequirement
    from ..models.operation_workflow_policy_requirement import OperationWorkflowPolicyRequirement
    from ..models.operation_workflow_retained_milestone_freshness_rule import (
        OperationWorkflowRetainedMilestoneFreshnessRule,
    )
    from ..models.operation_workflow_retained_milestone_group import OperationWorkflowRetainedMilestoneGroup
    from ..models.operation_workflow_retained_milestone_invalidation_rule import (
        OperationWorkflowRetainedMilestoneInvalidationRule,
    )
    from ..models.operation_workflow_retained_milestone_requirement import OperationWorkflowRetainedMilestoneRequirement


T = TypeVar("T", bound="OperationWorkflowTransition")


@_attrs_define
class OperationWorkflowTransition:
    """Allowed state edge declared by a pinned application workflow definition."""

    from_: str
    to: str
    retained_milestone_groups: list[OperationWorkflowRetainedMilestoneGroup] | Unset = UNSET
    retained_milestone_freshness: list[OperationWorkflowRetainedMilestoneFreshnessRule] | Unset = UNSET
    """Maximum evidence ages pinned to this workflow edge."""
    retained_milestone_invalidations: list[OperationWorkflowRetainedMilestoneInvalidationRule] | Unset = UNSET
    """Pinned edge rules linking retained prerequisites to invalidating facts from declared workflow steps.
    Business occurrence time determines order; an invalidating fact wins equal timestamps."""
    required_retained_milestones: list[OperationWorkflowRetainedMilestoneRequirement] | Unset = UNSET
    """Previously reported transaction-backed workflow facts required within the same ownership, subject, instance,
    and contract version; proposed evidence does not satisfy these prerequisites."""
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

        retained_milestone_groups: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.retained_milestone_groups, Unset):
            retained_milestone_groups = []
            for retained_milestone_groups_item_data in self.retained_milestone_groups:
                retained_milestone_groups_item = retained_milestone_groups_item_data.to_dict()
                retained_milestone_groups.append(retained_milestone_groups_item)

        retained_milestone_freshness: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.retained_milestone_freshness, Unset):
            retained_milestone_freshness = []
            for retained_milestone_freshness_item_data in self.retained_milestone_freshness:
                retained_milestone_freshness_item = retained_milestone_freshness_item_data.to_dict()
                retained_milestone_freshness.append(retained_milestone_freshness_item)

        retained_milestone_invalidations: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.retained_milestone_invalidations, Unset):
            retained_milestone_invalidations = []
            for retained_milestone_invalidations_item_data in self.retained_milestone_invalidations:
                retained_milestone_invalidations_item = retained_milestone_invalidations_item_data.to_dict()
                retained_milestone_invalidations.append(retained_milestone_invalidations_item)

        required_retained_milestones: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.required_retained_milestones, Unset):
            required_retained_milestones = []
            for required_retained_milestones_item_data in self.required_retained_milestones:
                required_retained_milestones_item = required_retained_milestones_item_data.to_dict()
                required_retained_milestones.append(required_retained_milestones_item)

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
            }
        )
        if retained_milestone_groups is not UNSET:
            field_dict["retained_milestone_groups"] = retained_milestone_groups
        if retained_milestone_freshness is not UNSET:
            field_dict["retained_milestone_freshness"] = retained_milestone_freshness
        if retained_milestone_invalidations is not UNSET:
            field_dict["retained_milestone_invalidations"] = retained_milestone_invalidations
        if required_retained_milestones is not UNSET:
            field_dict["required_retained_milestones"] = required_retained_milestones
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
        from ..models.operation_workflow_retained_milestone_freshness_rule import (
            OperationWorkflowRetainedMilestoneFreshnessRule,
        )
        from ..models.operation_workflow_retained_milestone_group import OperationWorkflowRetainedMilestoneGroup
        from ..models.operation_workflow_retained_milestone_invalidation_rule import (
            OperationWorkflowRetainedMilestoneInvalidationRule,
        )
        from ..models.operation_workflow_retained_milestone_requirement import (
            OperationWorkflowRetainedMilestoneRequirement,
        )

        d = dict(src_dict)
        from_ = d.pop("from")

        to = d.pop("to")

        _retained_milestone_groups = d.pop("retained_milestone_groups", UNSET)
        retained_milestone_groups: list[OperationWorkflowRetainedMilestoneGroup] | Unset = UNSET
        if _retained_milestone_groups is not UNSET:
            retained_milestone_groups = []
            for retained_milestone_groups_item_data in _retained_milestone_groups:
                retained_milestone_groups_item = OperationWorkflowRetainedMilestoneGroup.from_dict(
                    retained_milestone_groups_item_data
                )

                retained_milestone_groups.append(retained_milestone_groups_item)

        _retained_milestone_freshness = d.pop("retained_milestone_freshness", UNSET)
        retained_milestone_freshness: list[OperationWorkflowRetainedMilestoneFreshnessRule] | Unset = UNSET
        if _retained_milestone_freshness is not UNSET:
            retained_milestone_freshness = []
            for retained_milestone_freshness_item_data in _retained_milestone_freshness:
                retained_milestone_freshness_item = OperationWorkflowRetainedMilestoneFreshnessRule.from_dict(
                    retained_milestone_freshness_item_data
                )

                retained_milestone_freshness.append(retained_milestone_freshness_item)

        _retained_milestone_invalidations = d.pop("retained_milestone_invalidations", UNSET)
        retained_milestone_invalidations: list[OperationWorkflowRetainedMilestoneInvalidationRule] | Unset = UNSET
        if _retained_milestone_invalidations is not UNSET:
            retained_milestone_invalidations = []
            for retained_milestone_invalidations_item_data in _retained_milestone_invalidations:
                retained_milestone_invalidations_item = OperationWorkflowRetainedMilestoneInvalidationRule.from_dict(
                    retained_milestone_invalidations_item_data
                )

                retained_milestone_invalidations.append(retained_milestone_invalidations_item)

        _required_retained_milestones = d.pop("required_retained_milestones", UNSET)
        required_retained_milestones: list[OperationWorkflowRetainedMilestoneRequirement] | Unset = UNSET
        if _required_retained_milestones is not UNSET:
            required_retained_milestones = []
            for required_retained_milestones_item_data in _required_retained_milestones:
                required_retained_milestones_item = OperationWorkflowRetainedMilestoneRequirement.from_dict(
                    required_retained_milestones_item_data
                )

                required_retained_milestones.append(required_retained_milestones_item)

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

        operation_workflow_transition = cls(
            from_=from_,
            to=to,
            retained_milestone_groups=retained_milestone_groups,
            retained_milestone_freshness=retained_milestone_freshness,
            retained_milestone_invalidations=retained_milestone_invalidations,
            required_retained_milestones=required_retained_milestones,
            required_effects=required_effects,
            required_invariants=required_invariants,
            required_dependency_workflows=required_dependency_workflows,
            required_policies=required_policies,
            required_milestones=required_milestones,
        )

        return operation_workflow_transition
