from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_dependent_instance_dependency_status import (
    OperationWorkflowDependentInstanceDependencyStatus,
    check_operation_workflow_dependent_instance_dependency_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_subject import OperationSubject
    from ..models.operation_workflow_state import OperationWorkflowState


T = TypeVar("T", bound="OperationWorkflowDependentInstance")


@_attrs_define
class OperationWorkflowDependentInstance:
    """Current retained source pointing at the selected prerequisite. Status describes that prerequisite against this
    source's required outcome. Needs attention is true only for an active source with an unmet prerequisite.

    """

    subject: OperationSubject
    """Immutable public business correlation metadata. Captured at admission and preserved through recovery and
    redeploy. Never an ownership or authorization claim."""
    state: OperationWorkflowState
    """Latest app-reported state for one declared workflow instance, including terminal and staleness indicators."""
    dependency_status: OperationWorkflowDependentInstanceDependencyStatus
    needs_attention: bool
    required_outcome_code: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        subject = self.subject.to_dict()

        state = self.state.to_dict()

        dependency_status: str = self.dependency_status

        needs_attention = self.needs_attention

        required_outcome_code = self.required_outcome_code

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subject": subject,
                "state": state,
                "dependency_status": dependency_status,
                "needs_attention": needs_attention,
            }
        )
        if required_outcome_code is not UNSET:
            field_dict["required_outcome_code"] = required_outcome_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_subject import OperationSubject
        from ..models.operation_workflow_state import OperationWorkflowState

        d = dict(src_dict)
        subject = OperationSubject.from_dict(d.pop("subject"))

        state = OperationWorkflowState.from_dict(d.pop("state"))

        dependency_status = check_operation_workflow_dependent_instance_dependency_status(d.pop("dependency_status"))

        needs_attention = d.pop("needs_attention")

        required_outcome_code = d.pop("required_outcome_code", UNSET)

        operation_workflow_dependent_instance = cls(
            subject=subject,
            state=state,
            dependency_status=dependency_status,
            needs_attention=needs_attention,
            required_outcome_code=required_outcome_code,
        )

        return operation_workflow_dependent_instance
