from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_milestone_request import OperationMilestoneRequest
    from ..models.operation_workflow_state_report import OperationWorkflowStateReport


T = TypeVar("T", bound="OperationWorkflowStateValidationRequest")


@_attrs_define
class OperationWorkflowStateValidationRequest:
    """Batch of application-reported workflow updates to validate before their transaction commits."""

    workflow_states: list[OperationWorkflowStateReport]
    milestones: list[OperationMilestoneRequest] | Unset = UNSET
    """Facts from the same application transaction, used to verify transition evidence before commit."""

    def to_dict(self) -> dict[str, Any]:
        workflow_states = []
        for workflow_states_item_data in self.workflow_states:
            workflow_states_item = workflow_states_item_data.to_dict()
            workflow_states.append(workflow_states_item)

        milestones: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.milestones, Unset):
            milestones = []
            for milestones_item_data in self.milestones:
                milestones_item = milestones_item_data.to_dict()
                milestones.append(milestones_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow_states": workflow_states,
            }
        )
        if milestones is not UNSET:
            field_dict["milestones"] = milestones

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_milestone_request import OperationMilestoneRequest
        from ..models.operation_workflow_state_report import OperationWorkflowStateReport

        d = dict(src_dict)
        workflow_states = []
        _workflow_states = d.pop("workflow_states")
        for workflow_states_item_data in _workflow_states:
            workflow_states_item = OperationWorkflowStateReport.from_dict(workflow_states_item_data)

            workflow_states.append(workflow_states_item)

        _milestones = d.pop("milestones", UNSET)
        milestones: list[OperationMilestoneRequest] | Unset = UNSET
        if _milestones is not UNSET:
            milestones = []
            for milestones_item_data in _milestones:
                milestones_item = OperationMilestoneRequest.from_dict(milestones_item_data)

                milestones.append(milestones_item)

        operation_workflow_state_validation_request = cls(
            workflow_states=workflow_states,
            milestones=milestones,
        )

        return operation_workflow_state_validation_request
