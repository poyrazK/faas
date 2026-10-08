from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.operation_workflow_transition_readiness_advisories_item import (
    OperationWorkflowTransitionReadinessAdvisoriesItem,
    check_operation_workflow_transition_readiness_advisories_item,
)
from ..models.operation_workflow_transition_readiness_reasons_item import (
    OperationWorkflowTransitionReadinessReasonsItem,
    check_operation_workflow_transition_readiness_reasons_item,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_blocker import OperationWorkflowBlocker
    from ..models.operation_workflow_instance_transition import OperationWorkflowInstanceTransition
    from ..models.operation_workflow_policy_requirement import OperationWorkflowPolicyRequirement
    from ..models.operation_workflow_related_instance import OperationWorkflowRelatedInstance
    from ..models.operation_workflow_unmet_effect import OperationWorkflowUnmetEffect
    from ..models.operation_workflow_unmet_invariant import OperationWorkflowUnmetInvariant


T = TypeVar("T", bound="OperationWorkflowTransitionReadiness")


@_attrs_define
class OperationWorkflowTransitionReadiness:
    """Ready means declared requirements match retained reports and the proposed milestone plan. It does not authorize or
    commit a transition or validate milestone payloads. All reported workflow prerequisites apply; only blockers
    targeting this Operation apply. Staleness and overdue deadlines are advisories rather than undeclared guards.

    """

    transition: OperationWorkflowInstanceTransition
    """One allowed state edge from the selected contract version, bound to the Operation that can report it.
    Required milestones must be committed with the transition in the same application transaction. This declaration
    does not establish that the edge is currently valid for the application's business row or authorized for the
    caller."""
    declared: bool
    ready: bool
    reasons: list[OperationWorkflowTransitionReadinessReasonsItem]
    advisories: list[OperationWorkflowTransitionReadinessAdvisoriesItem]
    contract_version: int
    blockers: list[OperationWorkflowBlocker]
    unmet_dependencies: list[OperationWorkflowRelatedInstance]
    """Unmet reference/status pairs; inspect full related states through the workflow detail."""
    missing_milestones: list[str]
    unmet_effects: list[OperationWorkflowUnmetEffect] | Unset = UNSET
    unmet_invariants: list[OperationWorkflowUnmetInvariant] | Unset = UNSET
    invariant_blockers: list[OperationWorkflowBlocker] | Unset = UNSET
    """Subset of matching blockers using the invariant- namespace. Failed or unknown application checks deny
    readiness through application_blocked."""
    missing_dependency_workflows: list[str] | Unset = UNSET
    """Required workflow selectors without a reported prerequisite link."""
    missing_policies: list[OperationWorkflowPolicyRequirement] | Unset = UNSET
    state_revision: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        transition = self.transition.to_dict()

        declared = self.declared

        ready = self.ready

        reasons = []
        for reasons_item_data in self.reasons:
            reasons_item: str = reasons_item_data
            reasons.append(reasons_item)

        advisories = []
        for advisories_item_data in self.advisories:
            advisories_item: str = advisories_item_data
            advisories.append(advisories_item)

        contract_version = self.contract_version

        blockers = []
        for blockers_item_data in self.blockers:
            blockers_item = blockers_item_data.to_dict()
            blockers.append(blockers_item)

        unmet_dependencies = []
        for unmet_dependencies_item_data in self.unmet_dependencies:
            unmet_dependencies_item = unmet_dependencies_item_data.to_dict()
            unmet_dependencies.append(unmet_dependencies_item)

        missing_milestones = self.missing_milestones

        unmet_effects: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.unmet_effects, Unset):
            unmet_effects = []
            for unmet_effects_item_data in self.unmet_effects:
                unmet_effects_item = unmet_effects_item_data.to_dict()
                unmet_effects.append(unmet_effects_item)

        unmet_invariants: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.unmet_invariants, Unset):
            unmet_invariants = []
            for unmet_invariants_item_data in self.unmet_invariants:
                unmet_invariants_item = unmet_invariants_item_data.to_dict()
                unmet_invariants.append(unmet_invariants_item)

        invariant_blockers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.invariant_blockers, Unset):
            invariant_blockers = []
            for invariant_blockers_item_data in self.invariant_blockers:
                invariant_blockers_item = invariant_blockers_item_data.to_dict()
                invariant_blockers.append(invariant_blockers_item)

        missing_dependency_workflows: list[str] | Unset = UNSET
        if not isinstance(self.missing_dependency_workflows, Unset):
            missing_dependency_workflows = self.missing_dependency_workflows

        missing_policies: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.missing_policies, Unset):
            missing_policies = []
            for missing_policies_item_data in self.missing_policies:
                missing_policies_item = missing_policies_item_data.to_dict()
                missing_policies.append(missing_policies_item)

        state_revision = self.state_revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "transition": transition,
                "declared": declared,
                "ready": ready,
                "reasons": reasons,
                "advisories": advisories,
                "contract_version": contract_version,
                "blockers": blockers,
                "unmet_dependencies": unmet_dependencies,
                "missing_milestones": missing_milestones,
            }
        )
        if unmet_effects is not UNSET:
            field_dict["unmet_effects"] = unmet_effects
        if unmet_invariants is not UNSET:
            field_dict["unmet_invariants"] = unmet_invariants
        if invariant_blockers is not UNSET:
            field_dict["invariant_blockers"] = invariant_blockers
        if missing_dependency_workflows is not UNSET:
            field_dict["missing_dependency_workflows"] = missing_dependency_workflows
        if missing_policies is not UNSET:
            field_dict["missing_policies"] = missing_policies
        if state_revision is not UNSET:
            field_dict["state_revision"] = state_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_blocker import OperationWorkflowBlocker
        from ..models.operation_workflow_instance_transition import OperationWorkflowInstanceTransition
        from ..models.operation_workflow_policy_requirement import OperationWorkflowPolicyRequirement
        from ..models.operation_workflow_related_instance import OperationWorkflowRelatedInstance
        from ..models.operation_workflow_unmet_effect import OperationWorkflowUnmetEffect
        from ..models.operation_workflow_unmet_invariant import OperationWorkflowUnmetInvariant

        d = dict(src_dict)
        transition = OperationWorkflowInstanceTransition.from_dict(d.pop("transition"))

        declared = d.pop("declared")

        ready = d.pop("ready")

        reasons = []
        _reasons = d.pop("reasons")
        for reasons_item_data in _reasons:
            reasons_item = check_operation_workflow_transition_readiness_reasons_item(reasons_item_data)

            reasons.append(reasons_item)

        advisories = []
        _advisories = d.pop("advisories")
        for advisories_item_data in _advisories:
            advisories_item = check_operation_workflow_transition_readiness_advisories_item(advisories_item_data)

            advisories.append(advisories_item)

        contract_version = d.pop("contract_version")

        blockers = []
        _blockers = d.pop("blockers")
        for blockers_item_data in _blockers:
            blockers_item = OperationWorkflowBlocker.from_dict(blockers_item_data)

            blockers.append(blockers_item)

        unmet_dependencies = []
        _unmet_dependencies = d.pop("unmet_dependencies")
        for unmet_dependencies_item_data in _unmet_dependencies:
            unmet_dependencies_item = OperationWorkflowRelatedInstance.from_dict(unmet_dependencies_item_data)

            unmet_dependencies.append(unmet_dependencies_item)

        missing_milestones = cast(list[str], d.pop("missing_milestones"))

        _unmet_effects = d.pop("unmet_effects", UNSET)
        unmet_effects: list[OperationWorkflowUnmetEffect] | Unset = UNSET
        if _unmet_effects is not UNSET:
            unmet_effects = []
            for unmet_effects_item_data in _unmet_effects:
                unmet_effects_item = OperationWorkflowUnmetEffect.from_dict(unmet_effects_item_data)

                unmet_effects.append(unmet_effects_item)

        _unmet_invariants = d.pop("unmet_invariants", UNSET)
        unmet_invariants: list[OperationWorkflowUnmetInvariant] | Unset = UNSET
        if _unmet_invariants is not UNSET:
            unmet_invariants = []
            for unmet_invariants_item_data in _unmet_invariants:
                unmet_invariants_item = OperationWorkflowUnmetInvariant.from_dict(unmet_invariants_item_data)

                unmet_invariants.append(unmet_invariants_item)

        _invariant_blockers = d.pop("invariant_blockers", UNSET)
        invariant_blockers: list[OperationWorkflowBlocker] | Unset = UNSET
        if _invariant_blockers is not UNSET:
            invariant_blockers = []
            for invariant_blockers_item_data in _invariant_blockers:
                invariant_blockers_item = OperationWorkflowBlocker.from_dict(invariant_blockers_item_data)

                invariant_blockers.append(invariant_blockers_item)

        missing_dependency_workflows = cast(list[str], d.pop("missing_dependency_workflows", UNSET))

        _missing_policies = d.pop("missing_policies", UNSET)
        missing_policies: list[OperationWorkflowPolicyRequirement] | Unset = UNSET
        if _missing_policies is not UNSET:
            missing_policies = []
            for missing_policies_item_data in _missing_policies:
                missing_policies_item = OperationWorkflowPolicyRequirement.from_dict(missing_policies_item_data)

                missing_policies.append(missing_policies_item)

        state_revision = d.pop("state_revision", UNSET)

        operation_workflow_transition_readiness = cls(
            transition=transition,
            declared=declared,
            ready=ready,
            reasons=reasons,
            advisories=advisories,
            contract_version=contract_version,
            blockers=blockers,
            unmet_dependencies=unmet_dependencies,
            missing_milestones=missing_milestones,
            unmet_effects=unmet_effects,
            unmet_invariants=unmet_invariants,
            invariant_blockers=invariant_blockers,
            missing_dependency_workflows=missing_dependency_workflows,
            missing_policies=missing_policies,
            state_revision=state_revision,
        )

        return operation_workflow_transition_readiness
