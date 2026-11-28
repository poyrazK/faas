from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_related_instance_status import (
    OperationWorkflowRelatedInstanceStatus,
    check_operation_workflow_related_instance_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_dependency import OperationWorkflowDependency
    from ..models.operation_workflow_state import OperationWorkflowState


T = TypeVar("T", bound="OperationWorkflowRelatedInstance")


@_attrs_define
class OperationWorkflowRelatedInstance:
    """One-hop retained state resolution. Terminal without a required outcome does not establish business success."""

    dependency: OperationWorkflowDependency
    """Direct prerequisite in the same application/customer/environment. References may have no retained state.
    Self references and duplicate targets are rejected."""
    status: OperationWorkflowRelatedInstanceStatus
    state: OperationWorkflowState | Unset = UNSET
    """Latest app-reported state for one declared workflow instance, including terminal and staleness indicators."""

    def to_dict(self) -> dict[str, Any]:
        dependency = self.dependency.to_dict()

        status: str = self.status

        state: dict[str, Any] | Unset = UNSET
        if not isinstance(self.state, Unset):
            state = self.state.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "dependency": dependency,
                "status": status,
            }
        )
        if state is not UNSET:
            field_dict["state"] = state

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_dependency import OperationWorkflowDependency
        from ..models.operation_workflow_state import OperationWorkflowState

        d = dict(src_dict)
        dependency = OperationWorkflowDependency.from_dict(d.pop("dependency"))

        status = check_operation_workflow_related_instance_status(d.pop("status"))

        _state = d.pop("state", UNSET)
        state: OperationWorkflowState | Unset
        if isinstance(_state, Unset):
            state = UNSET
        else:
            state = OperationWorkflowState.from_dict(_state)

        operation_workflow_related_instance = cls(
            dependency=dependency,
            status=status,
            state=state,
        )

        return operation_workflow_related_instance
