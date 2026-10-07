from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_state_report import OperationWorkflowStateReport


T = TypeVar("T", bound="OperationWorkflowStateValidationRequest")


@_attrs_define
class OperationWorkflowStateValidationRequest:
    workflow_states: list[OperationWorkflowStateReport]

    def to_dict(self) -> dict[str, Any]:
        workflow_states = []
        for workflow_states_item_data in self.workflow_states:
            workflow_states_item = workflow_states_item_data.to_dict()
            workflow_states.append(workflow_states_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workflow_states": workflow_states,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_state_report import OperationWorkflowStateReport

        d = dict(src_dict)
        workflow_states = []
        _workflow_states = d.pop("workflow_states")
        for workflow_states_item_data in _workflow_states:
            workflow_states_item = OperationWorkflowStateReport.from_dict(workflow_states_item_data)

            workflow_states.append(workflow_states_item)

        operation_workflow_state_validation_request = cls(
            workflow_states=workflow_states,
        )

        return operation_workflow_state_validation_request
