from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_bottlenecks import OperationWorkflowBottlenecks
    from ..models.operation_workflow_decision import OperationWorkflowDecision
    from ..models.operation_workflow_dependency_impact import OperationWorkflowDependencyImpact
    from ..models.operation_workflow_dependency_trace import OperationWorkflowDependencyTrace
    from ..models.operation_workflow_instance_step import OperationWorkflowInstanceStep
    from ..models.operation_workflow_instance_transition import OperationWorkflowInstanceTransition
    from ..models.operation_workflow_readiness_overview import OperationWorkflowReadinessOverview
    from ..models.operation_workflow_related_instance import OperationWorkflowRelatedInstance
    from ..models.operation_workflow_resolution_verification import OperationWorkflowResolutionVerification
    from ..models.operation_workflow_state import OperationWorkflowState
    from ..models.operation_workflow_state_history_entry import OperationWorkflowStateHistoryEntry


T = TypeVar("T", bound="OperationWorkflowInstanceSnapshot")


@_attrs_define
class OperationWorkflowInstanceSnapshot:
    """Grouped view of the selected contract's declared steps and allowed transitions, current explicit state, and
    transition-history page for one workflow instance. Allowed transitions are contract edges by target Operation; the
    application still checks its business row and authorization before using one. Page-scoped facts follow the milestone
    cursor; retention-wide step summaries cover all matching facts still retained under the normal Operation retention
    rules.

    """

    workflow: str
    instance_id: str
    contract_version: int
    steps: list[OperationWorkflowInstanceStep]
    transitions: list[OperationWorkflowStateHistoryEntry]
    has_more: bool
    """True when either milestone or transition-history cursor has more pages."""
    bottlenecks: OperationWorkflowBottlenecks | Unset = UNSET
    """Observed durations for one retained workflow instance. State/blocker intervals use application occurrence
    time. Verification waits use platform publication time. Gaps are excluded and incomplete histories are marked.
    Groups are sorted by observed duration and bounded independently of exact window totals."""
    resolution_verifications: list[OperationWorkflowResolutionVerification] | Unset = UNSET
    """Bounded preview with pending obligations first. Exact counts cover all retained distinct obligations."""
    awaiting_verification_count: int | Unset = UNSET
    resolution_verification_count: int | Unset = UNSET
    readiness: OperationWorkflowReadinessOverview | Unset = UNSET
    """At most 100 current-state declared edges evaluated without planned milestones. Counts cover all declared
    current-state edges; history cursors do not paginate this overview."""
    dependency_trace: OperationWorkflowDependencyTrace | Unset = UNSET
    """Bounded deterministic depth-first traversal of unmet reported prerequisites within one
    customer/application/environment. Unknown retained state and traversal limits are distinct. Terminal sources and
    satisfied requirements stop traversal. Current reports may change while reading."""
    dependency_impact: OperationWorkflowDependencyImpact | Unset = UNSET
    """One-hop reverse dependency impact within the selected customer/application/environment. Counts cover all
    current retained sources; items are capped at 100 with affected sources first. Omitted when no customer can be
    identified for an unknown account-side prerequisite. Independent of milestone and history pagination."""
    related_workflows: list[OperationWorkflowRelatedInstance] | Unset = UNSET
    decision: OperationWorkflowDecision | Unset = UNSET
    """Observational explanation from the selected contract and latest reported state, independent of fact
    pagination. These options are not execution authorization. Required milestones must be committed with the next
    transition; retained historical facts do not satisfy them."""
    state: OperationWorkflowState | Unset = UNSET
    """Latest app-reported state for one declared workflow instance, including terminal and staleness indicators."""
    allowed_transitions: list[OperationWorkflowInstanceTransition] | Unset = UNSET
    next_milestone_cursor: str | Unset = UNSET
    next_transition_cursor: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        workflow = self.workflow

        instance_id = self.instance_id

        contract_version = self.contract_version

        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        transitions = []
        for transitions_item_data in self.transitions:
            transitions_item = transitions_item_data.to_dict()
            transitions.append(transitions_item)

        has_more = self.has_more

        bottlenecks: dict[str, Any] | Unset = UNSET
        if not isinstance(self.bottlenecks, Unset):
            bottlenecks = self.bottlenecks.to_dict()

        resolution_verifications: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.resolution_verifications, Unset):
            resolution_verifications = []
            for resolution_verifications_item_data in self.resolution_verifications:
                resolution_verifications_item = resolution_verifications_item_data.to_dict()
                resolution_verifications.append(resolution_verifications_item)

        awaiting_verification_count = self.awaiting_verification_count

        resolution_verification_count = self.resolution_verification_count

        readiness: dict[str, Any] | Unset = UNSET
        if not isinstance(self.readiness, Unset):
            readiness = self.readiness.to_dict()

        dependency_trace: dict[str, Any] | Unset = UNSET
        if not isinstance(self.dependency_trace, Unset):
            dependency_trace = self.dependency_trace.to_dict()

        dependency_impact: dict[str, Any] | Unset = UNSET
        if not isinstance(self.dependency_impact, Unset):
            dependency_impact = self.dependency_impact.to_dict()

        related_workflows: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.related_workflows, Unset):
            related_workflows = []
            for related_workflows_item_data in self.related_workflows:
                related_workflows_item = related_workflows_item_data.to_dict()
                related_workflows.append(related_workflows_item)

        decision: dict[str, Any] | Unset = UNSET
        if not isinstance(self.decision, Unset):
            decision = self.decision.to_dict()

        state: dict[str, Any] | Unset = UNSET
        if not isinstance(self.state, Unset):
            state = self.state.to_dict()

        allowed_transitions: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.allowed_transitions, Unset):
            allowed_transitions = []
            for allowed_transitions_item_data in self.allowed_transitions:
                allowed_transitions_item = allowed_transitions_item_data.to_dict()
                allowed_transitions.append(allowed_transitions_item)

        next_milestone_cursor = self.next_milestone_cursor

        next_transition_cursor = self.next_transition_cursor

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow": workflow,
                "instance_id": instance_id,
                "contract_version": contract_version,
                "steps": steps,
                "transitions": transitions,
                "has_more": has_more,
            }
        )
        if bottlenecks is not UNSET:
            field_dict["bottlenecks"] = bottlenecks
        if resolution_verifications is not UNSET:
            field_dict["resolution_verifications"] = resolution_verifications
        if awaiting_verification_count is not UNSET:
            field_dict["awaiting_verification_count"] = awaiting_verification_count
        if resolution_verification_count is not UNSET:
            field_dict["resolution_verification_count"] = resolution_verification_count
        if readiness is not UNSET:
            field_dict["readiness"] = readiness
        if dependency_trace is not UNSET:
            field_dict["dependency_trace"] = dependency_trace
        if dependency_impact is not UNSET:
            field_dict["dependency_impact"] = dependency_impact
        if related_workflows is not UNSET:
            field_dict["related_workflows"] = related_workflows
        if decision is not UNSET:
            field_dict["decision"] = decision
        if state is not UNSET:
            field_dict["state"] = state
        if allowed_transitions is not UNSET:
            field_dict["allowed_transitions"] = allowed_transitions
        if next_milestone_cursor is not UNSET:
            field_dict["next_milestone_cursor"] = next_milestone_cursor
        if next_transition_cursor is not UNSET:
            field_dict["next_transition_cursor"] = next_transition_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_bottlenecks import OperationWorkflowBottlenecks
        from ..models.operation_workflow_decision import OperationWorkflowDecision
        from ..models.operation_workflow_dependency_impact import OperationWorkflowDependencyImpact
        from ..models.operation_workflow_dependency_trace import OperationWorkflowDependencyTrace
        from ..models.operation_workflow_instance_step import OperationWorkflowInstanceStep
        from ..models.operation_workflow_instance_transition import OperationWorkflowInstanceTransition
        from ..models.operation_workflow_readiness_overview import OperationWorkflowReadinessOverview
        from ..models.operation_workflow_related_instance import OperationWorkflowRelatedInstance
        from ..models.operation_workflow_resolution_verification import OperationWorkflowResolutionVerification
        from ..models.operation_workflow_state import OperationWorkflowState
        from ..models.operation_workflow_state_history_entry import OperationWorkflowStateHistoryEntry

        d = dict(src_dict)
        workflow = d.pop("workflow")

        instance_id = d.pop("instance_id")

        contract_version = d.pop("contract_version")

        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = OperationWorkflowInstanceStep.from_dict(steps_item_data)

            steps.append(steps_item)

        transitions = []
        _transitions = d.pop("transitions")
        for transitions_item_data in _transitions:
            transitions_item = OperationWorkflowStateHistoryEntry.from_dict(transitions_item_data)

            transitions.append(transitions_item)

        has_more = d.pop("has_more")

        _bottlenecks = d.pop("bottlenecks", UNSET)
        bottlenecks: OperationWorkflowBottlenecks | Unset
        if isinstance(_bottlenecks, Unset):
            bottlenecks = UNSET
        else:
            bottlenecks = OperationWorkflowBottlenecks.from_dict(_bottlenecks)

        _resolution_verifications = d.pop("resolution_verifications", UNSET)
        resolution_verifications: list[OperationWorkflowResolutionVerification] | Unset = UNSET
        if _resolution_verifications is not UNSET:
            resolution_verifications = []
            for resolution_verifications_item_data in _resolution_verifications:
                resolution_verifications_item = OperationWorkflowResolutionVerification.from_dict(
                    resolution_verifications_item_data
                )

                resolution_verifications.append(resolution_verifications_item)

        awaiting_verification_count = d.pop("awaiting_verification_count", UNSET)

        resolution_verification_count = d.pop("resolution_verification_count", UNSET)

        _readiness = d.pop("readiness", UNSET)
        readiness: OperationWorkflowReadinessOverview | Unset
        if isinstance(_readiness, Unset):
            readiness = UNSET
        else:
            readiness = OperationWorkflowReadinessOverview.from_dict(_readiness)

        _dependency_trace = d.pop("dependency_trace", UNSET)
        dependency_trace: OperationWorkflowDependencyTrace | Unset
        if isinstance(_dependency_trace, Unset):
            dependency_trace = UNSET
        else:
            dependency_trace = OperationWorkflowDependencyTrace.from_dict(_dependency_trace)

        _dependency_impact = d.pop("dependency_impact", UNSET)
        dependency_impact: OperationWorkflowDependencyImpact | Unset
        if isinstance(_dependency_impact, Unset):
            dependency_impact = UNSET
        else:
            dependency_impact = OperationWorkflowDependencyImpact.from_dict(_dependency_impact)

        _related_workflows = d.pop("related_workflows", UNSET)
        related_workflows: list[OperationWorkflowRelatedInstance] | Unset = UNSET
        if _related_workflows is not UNSET:
            related_workflows = []
            for related_workflows_item_data in _related_workflows:
                related_workflows_item = OperationWorkflowRelatedInstance.from_dict(related_workflows_item_data)

                related_workflows.append(related_workflows_item)

        _decision = d.pop("decision", UNSET)
        decision: OperationWorkflowDecision | Unset
        if isinstance(_decision, Unset):
            decision = UNSET
        else:
            decision = OperationWorkflowDecision.from_dict(_decision)

        _state = d.pop("state", UNSET)
        state: OperationWorkflowState | Unset
        if isinstance(_state, Unset):
            state = UNSET
        else:
            state = OperationWorkflowState.from_dict(_state)

        _allowed_transitions = d.pop("allowed_transitions", UNSET)
        allowed_transitions: list[OperationWorkflowInstanceTransition] | Unset = UNSET
        if _allowed_transitions is not UNSET:
            allowed_transitions = []
            for allowed_transitions_item_data in _allowed_transitions:
                allowed_transitions_item = OperationWorkflowInstanceTransition.from_dict(allowed_transitions_item_data)

                allowed_transitions.append(allowed_transitions_item)

        next_milestone_cursor = d.pop("next_milestone_cursor", UNSET)

        next_transition_cursor = d.pop("next_transition_cursor", UNSET)

        operation_workflow_instance_snapshot = cls(
            workflow=workflow,
            instance_id=instance_id,
            contract_version=contract_version,
            steps=steps,
            transitions=transitions,
            has_more=has_more,
            bottlenecks=bottlenecks,
            resolution_verifications=resolution_verifications,
            awaiting_verification_count=awaiting_verification_count,
            resolution_verification_count=resolution_verification_count,
            readiness=readiness,
            dependency_trace=dependency_trace,
            dependency_impact=dependency_impact,
            related_workflows=related_workflows,
            decision=decision,
            state=state,
            allowed_transitions=allowed_transitions,
            next_milestone_cursor=next_milestone_cursor,
            next_transition_cursor=next_transition_cursor,
        )

        return operation_workflow_instance_snapshot
