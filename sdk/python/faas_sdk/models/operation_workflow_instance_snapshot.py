from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset
from .operation_workflow_readiness_overview import OperationWorkflowReadinessOverview
from .operation_workflow_dependency_trace import OperationWorkflowDependencyTrace
from .operation_workflow_dependency_impact import OperationWorkflowDependencyImpact
from .operation_workflow_related_instance import OperationWorkflowRelatedInstance
from .operation_workflow_decision import OperationWorkflowDecision

if TYPE_CHECKING:
    from ..models.operation_workflow_instance_step import OperationWorkflowInstanceStep
    from ..models.operation_workflow_instance_transition import OperationWorkflowInstanceTransition
    from ..models.operation_workflow_state import OperationWorkflowState
    from ..models.operation_workflow_state_history_entry import OperationWorkflowStateHistoryEntry


T = TypeVar("T", bound="OperationWorkflowInstanceSnapshot")


@_attrs_define
class OperationWorkflowInstanceSnapshot:
    """Grouped view of the selected contract's declared steps and allowed transitions, current explicit state, and
    transition-history page for one workflow instance.
    Page-scoped facts follow the milestone cursor; retention-wide step summaries cover all matching facts still retained
    under the normal Operation retention rules.

    """

    workflow: str
    instance_id: str
    contract_version: int
    steps: list[OperationWorkflowInstanceStep]
    transitions: list[OperationWorkflowStateHistoryEntry]
    has_more: bool
    """True when either milestone or transition-history cursor has more pages."""
    readiness: OperationWorkflowReadinessOverview | Unset = UNSET
    dependency_trace: OperationWorkflowDependencyTrace | Unset = UNSET
    dependency_impact: OperationWorkflowDependencyImpact | Unset = UNSET
    related_workflows: list[OperationWorkflowRelatedInstance] | Unset = UNSET
    decision: OperationWorkflowDecision | Unset = UNSET
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

        allowed_transitions: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.allowed_transitions, Unset):
            allowed_transitions = []
            for allowed_transitions_item_data in self.allowed_transitions:
                allowed_transitions_item = allowed_transitions_item_data.to_dict()
                allowed_transitions.append(allowed_transitions_item)

        transitions = []
        for transitions_item_data in self.transitions:
            transitions_item = transitions_item_data.to_dict()
            transitions.append(transitions_item)

        has_more = self.has_more

        state: dict[str, Any] | Unset = UNSET
        if not isinstance(self.state, Unset):
            state = self.state.to_dict()

        next_milestone_cursor = self.next_milestone_cursor

        next_transition_cursor = self.next_transition_cursor

        field_dict: dict[str, Any] = {}
        if self.readiness is not UNSET: field_dict["readiness"] = self.readiness.to_dict()
        if self.dependency_trace is not UNSET: field_dict["dependency_trace"] = self.dependency_trace.to_dict()
        if self.dependency_impact is not UNSET: field_dict["dependency_impact"] = self.dependency_impact.to_dict()
        if self.related_workflows is not UNSET: field_dict["related_workflows"]=[v.to_dict() for v in self.related_workflows]

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
        if not isinstance(self.decision, Unset):
            field_dict["decision"] = self.decision.to_dict()
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
        from ..models.operation_workflow_instance_step import OperationWorkflowInstanceStep
        from ..models.operation_workflow_instance_transition import OperationWorkflowInstanceTransition
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

        _allowed_transitions = d.pop("allowed_transitions", UNSET)
        allowed_transitions: list[OperationWorkflowInstanceTransition] | Unset
        if isinstance(_allowed_transitions, Unset):
            allowed_transitions = UNSET
        else:
            allowed_transitions = []
            for allowed_transitions_item_data in _allowed_transitions:
                allowed_transitions_item = OperationWorkflowInstanceTransition.from_dict(allowed_transitions_item_data)

                allowed_transitions.append(allowed_transitions_item)

        transitions = []
        _transitions = d.pop("transitions")
        for transitions_item_data in _transitions:
            transitions_item = OperationWorkflowStateHistoryEntry.from_dict(transitions_item_data)

            transitions.append(transitions_item)

        has_more = d.pop("has_more")

        _state = d.pop("state", UNSET)
        state: OperationWorkflowState | Unset
        if isinstance(_state, Unset):
            state = UNSET
        else:
            state = OperationWorkflowState.from_dict(_state)

        next_milestone_cursor = d.pop("next_milestone_cursor", UNSET)

        next_transition_cursor = d.pop("next_transition_cursor", UNSET)

        operation_workflow_instance_snapshot = cls(
            readiness=OperationWorkflowReadinessOverview.from_dict(d.pop("readiness")) if d.get("readiness") is not None else UNSET,
            dependency_trace=OperationWorkflowDependencyTrace.from_dict(d.pop("dependency_trace")) if d.get("dependency_trace") is not None else UNSET,
            dependency_impact=OperationWorkflowDependencyImpact.from_dict(d.pop("dependency_impact")) if d.get("dependency_impact") is not None else UNSET,
            related_workflows=[OperationWorkflowRelatedInstance.from_dict(v) for v in d.pop("related_workflows")] if "related_workflows" in d else UNSET,
            decision=OperationWorkflowDecision.from_dict(d["decision"]) if "decision" in d else UNSET,
            workflow=workflow,
            instance_id=instance_id,
            contract_version=contract_version,
            steps=steps,
            transitions=transitions,
            has_more=has_more,
            state=state,
            allowed_transitions=allowed_transitions,
            next_milestone_cursor=next_milestone_cursor,
            next_transition_cursor=next_transition_cursor,
        )

        return operation_workflow_instance_snapshot
