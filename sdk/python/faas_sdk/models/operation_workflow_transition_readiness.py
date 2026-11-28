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
    from ..models.operation_workflow_resolution_verification import OperationWorkflowResolutionVerification
    from ..models.operation_workflow_retained_milestone import OperationWorkflowRetainedMilestone
    from ..models.operation_workflow_retained_milestone_group_result import (
        OperationWorkflowRetainedMilestoneGroupResult,
    )
    from ..models.operation_workflow_transition_reporting import OperationWorkflowTransitionReporting
    from ..models.operation_workflow_unmet_effect import OperationWorkflowUnmetEffect
    from ..models.operation_workflow_unmet_invariant import OperationWorkflowUnmetInvariant


T = TypeVar("T", bound="OperationWorkflowTransitionReadiness")


@_attrs_define
class OperationWorkflowTransitionReadiness:
    """Ready means declared requirements match retained reports and the proposed milestone plan. It does not authorize or
    commit a transition or validate milestone payloads. All reported workflow prerequisites apply; only blockers
    targeting this Operation apply. Staleness, overdue deadlines, current-state reporting context and pending resolution
    proofs are advisories rather than undeclared guards. Reporting context uses current pinned expected_states, not
    invented required transition conditions. Planned reports do not replace this retained context.

    """

    pending_verification_count: int
    """Exact retained pending resolution proof count targeting the transition Operation before previews, across
    retained workflow reports. Advisory rather than a new transition guard."""
    pending_verifications_truncated: bool
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
    reporting: OperationWorkflowTransitionReporting | Unset = UNSET
    """Workflow-wide current-state expected condition coverage evaluated from the latest reporting Operation pinned
    definition and retained explicit decision contexts. It does not add transition guards. Counts precede previews.
    Unknown coverage is never inferred as satisfied; source evidence uses its own pinned freshness budget.
    Conditions apply to current state rather than proposed destination state."""
    pending_verifications: list[OperationWorkflowResolutionVerification] | Unset = UNSET
    """Canonical first matching pending proofs with declared owners and milestone references. Verified proofs and
    other target Operations are excluded."""
    retained_milestone_groups: list[OperationWorkflowRetainedMilestoneGroupResult] | Unset = UNSET
    retained_milestones: list[OperationWorkflowRetainedMilestone] | Unset = UNSET
    """Status and latest retained evidence for each prerequisite declared on this edge. Absent facts remain
    awaiting verification; invalidating facts mark prerequisites invalidated. Both statuses prevent readiness."""
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
        pending_verification_count = self.pending_verification_count

        pending_verifications_truncated = self.pending_verifications_truncated

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

        reporting: dict[str, Any] | Unset = UNSET
        if not isinstance(self.reporting, Unset):
            reporting = self.reporting.to_dict()

        pending_verifications: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.pending_verifications, Unset):
            pending_verifications = []
            for pending_verifications_item_data in self.pending_verifications:
                pending_verifications_item = pending_verifications_item_data.to_dict()
                pending_verifications.append(pending_verifications_item)

        retained_milestone_groups: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.retained_milestone_groups, Unset):
            retained_milestone_groups = []
            for retained_milestone_groups_item_data in self.retained_milestone_groups:
                retained_milestone_groups_item = retained_milestone_groups_item_data.to_dict()
                retained_milestone_groups.append(retained_milestone_groups_item)

        retained_milestones: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.retained_milestones, Unset):
            retained_milestones = []
            for retained_milestones_item_data in self.retained_milestones:
                retained_milestones_item = retained_milestones_item_data.to_dict()
                retained_milestones.append(retained_milestones_item)

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
                "pending_verification_count": pending_verification_count,
                "pending_verifications_truncated": pending_verifications_truncated,
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
        if reporting is not UNSET:
            field_dict["reporting"] = reporting
        if pending_verifications is not UNSET:
            field_dict["pending_verifications"] = pending_verifications
        if retained_milestone_groups is not UNSET:
            field_dict["retained_milestone_groups"] = retained_milestone_groups
        if retained_milestones is not UNSET:
            field_dict["retained_milestones"] = retained_milestones
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
        from ..models.operation_workflow_resolution_verification import OperationWorkflowResolutionVerification
        from ..models.operation_workflow_retained_milestone import OperationWorkflowRetainedMilestone
        from ..models.operation_workflow_retained_milestone_group_result import (
            OperationWorkflowRetainedMilestoneGroupResult,
        )
        from ..models.operation_workflow_transition_reporting import OperationWorkflowTransitionReporting
        from ..models.operation_workflow_unmet_effect import OperationWorkflowUnmetEffect
        from ..models.operation_workflow_unmet_invariant import OperationWorkflowUnmetInvariant

        d = dict(src_dict)
        pending_verification_count = d.pop("pending_verification_count")

        pending_verifications_truncated = d.pop("pending_verifications_truncated")

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

        _reporting = d.pop("reporting", UNSET)
        reporting: OperationWorkflowTransitionReporting | Unset
        if isinstance(_reporting, Unset):
            reporting = UNSET
        else:
            reporting = OperationWorkflowTransitionReporting.from_dict(_reporting)

        _pending_verifications = d.pop("pending_verifications", UNSET)
        pending_verifications: list[OperationWorkflowResolutionVerification] | Unset = UNSET
        if _pending_verifications is not UNSET:
            pending_verifications = []
            for pending_verifications_item_data in _pending_verifications:
                pending_verifications_item = OperationWorkflowResolutionVerification.from_dict(
                    pending_verifications_item_data
                )

                pending_verifications.append(pending_verifications_item)

        _retained_milestone_groups = d.pop("retained_milestone_groups", UNSET)
        retained_milestone_groups: list[OperationWorkflowRetainedMilestoneGroupResult] | Unset = UNSET
        if _retained_milestone_groups is not UNSET:
            retained_milestone_groups = []
            for retained_milestone_groups_item_data in _retained_milestone_groups:
                retained_milestone_groups_item = OperationWorkflowRetainedMilestoneGroupResult.from_dict(
                    retained_milestone_groups_item_data
                )

                retained_milestone_groups.append(retained_milestone_groups_item)

        _retained_milestones = d.pop("retained_milestones", UNSET)
        retained_milestones: list[OperationWorkflowRetainedMilestone] | Unset = UNSET
        if _retained_milestones is not UNSET:
            retained_milestones = []
            for retained_milestones_item_data in _retained_milestones:
                retained_milestones_item = OperationWorkflowRetainedMilestone.from_dict(retained_milestones_item_data)

                retained_milestones.append(retained_milestones_item)

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
            pending_verification_count=pending_verification_count,
            pending_verifications_truncated=pending_verifications_truncated,
            transition=transition,
            declared=declared,
            ready=ready,
            reasons=reasons,
            advisories=advisories,
            contract_version=contract_version,
            blockers=blockers,
            unmet_dependencies=unmet_dependencies,
            missing_milestones=missing_milestones,
            reporting=reporting,
            pending_verifications=pending_verifications,
            retained_milestone_groups=retained_milestone_groups,
            retained_milestones=retained_milestones,
            unmet_effects=unmet_effects,
            unmet_invariants=unmet_invariants,
            invariant_blockers=invariant_blockers,
            missing_dependency_workflows=missing_dependency_workflows,
            missing_policies=missing_policies,
            state_revision=state_revision,
        )

        return operation_workflow_transition_readiness
