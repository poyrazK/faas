from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_milestone import OperationMilestone
    from ..models.operation_workflow_instance_snapshot import OperationWorkflowInstanceSnapshot
    from ..models.operation_workflow_state import OperationWorkflowState
    from ..models.operation_workflow_state_history_entry import OperationWorkflowStateHistoryEntry


T = TypeVar("T", bound="OperationMilestonesResponse")


@_attrs_define
class OperationMilestonesResponse:
    """Page of retained public milestone facts in the selected ownership and business-reference boundary."""

    milestones: list[OperationMilestone]
    workflow_states: list[OperationWorkflowState] | Unset = UNSET
    """Latest explicitly reported states for recent workflow instances under this business reference. With
    stale_only=true, this list contains only states past their app-declared age threshold. Milestone facts and state
    history are unaffected. This list is empty on Operation-specific feeds."""
    workflow_state_history: list[OperationWorkflowStateHistoryEntry] | Unset = UNSET
    """Retained app-reported changes for the exact workflow run when paired workflow selectors are supplied,
    ordered by app-assigned revision."""
    workflow_instance: OperationWorkflowInstanceSnapshot | Unset = UNSET
    """Grouped view of the declared steps, current explicit state, and transition-history page for one workflow
    instance. Page-scoped facts follow the milestone cursor; retention-wide step summaries cover all matching facts
    still retained under the normal Operation retention rules."""
    next_cursor: str | Unset = UNSET
    """Opaque continuation bound to the same account"""
    next_workflow_state_cursor: str | Unset = UNSET
    """Independent continuation for workflow_state_history, bound to the same run and ownership filters."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        milestones = []
        for milestones_item_data in self.milestones:
            milestones_item = milestones_item_data.to_dict()
            milestones.append(milestones_item)

        workflow_states: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.workflow_states, Unset):
            workflow_states = []
            for workflow_states_item_data in self.workflow_states:
                workflow_states_item = workflow_states_item_data.to_dict()
                workflow_states.append(workflow_states_item)

        workflow_state_history: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.workflow_state_history, Unset):
            workflow_state_history = []
            for workflow_state_history_item_data in self.workflow_state_history:
                workflow_state_history_item = workflow_state_history_item_data.to_dict()
                workflow_state_history.append(workflow_state_history_item)

        workflow_instance: dict[str, Any] | Unset = UNSET
        if not isinstance(self.workflow_instance, Unset):
            workflow_instance = self.workflow_instance.to_dict()

        next_cursor = self.next_cursor

        next_workflow_state_cursor = self.next_workflow_state_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "milestones": milestones,
            }
        )
        if workflow_states is not UNSET:
            field_dict["workflow_states"] = workflow_states
        if workflow_state_history is not UNSET:
            field_dict["workflow_state_history"] = workflow_state_history
        if workflow_instance is not UNSET:
            field_dict["workflow_instance"] = workflow_instance
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor
        if next_workflow_state_cursor is not UNSET:
            field_dict["next_workflow_state_cursor"] = next_workflow_state_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_milestone import OperationMilestone
        from ..models.operation_workflow_instance_snapshot import OperationWorkflowInstanceSnapshot
        from ..models.operation_workflow_state import OperationWorkflowState
        from ..models.operation_workflow_state_history_entry import OperationWorkflowStateHistoryEntry

        d = dict(src_dict)
        milestones = []
        _milestones = d.pop("milestones")
        for milestones_item_data in _milestones:
            milestones_item = OperationMilestone.from_dict(milestones_item_data)

            milestones.append(milestones_item)

        _workflow_states = d.pop("workflow_states", UNSET)
        workflow_states: list[OperationWorkflowState] | Unset = UNSET
        if _workflow_states is not UNSET:
            workflow_states = []
            for workflow_states_item_data in _workflow_states:
                workflow_states_item = OperationWorkflowState.from_dict(workflow_states_item_data)

                workflow_states.append(workflow_states_item)

        _workflow_state_history = d.pop("workflow_state_history", UNSET)
        workflow_state_history: list[OperationWorkflowStateHistoryEntry] | Unset = UNSET
        if _workflow_state_history is not UNSET:
            workflow_state_history = []
            for workflow_state_history_item_data in _workflow_state_history:
                workflow_state_history_item = OperationWorkflowStateHistoryEntry.from_dict(
                    workflow_state_history_item_data
                )

                workflow_state_history.append(workflow_state_history_item)

        _workflow_instance = d.pop("workflow_instance", UNSET)
        workflow_instance: OperationWorkflowInstanceSnapshot | Unset
        if isinstance(_workflow_instance, Unset):
            workflow_instance = UNSET
        else:
            workflow_instance = OperationWorkflowInstanceSnapshot.from_dict(_workflow_instance)

        next_cursor = d.pop("next_cursor", UNSET)

        next_workflow_state_cursor = d.pop("next_workflow_state_cursor", UNSET)

        operation_milestones_response = cls(
            milestones=milestones,
            workflow_states=workflow_states,
            workflow_state_history=workflow_state_history,
            workflow_instance=workflow_instance,
            next_cursor=next_cursor,
            next_workflow_state_cursor=next_workflow_state_cursor,
        )

        operation_milestones_response.additional_properties = d
        return operation_milestones_response

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
