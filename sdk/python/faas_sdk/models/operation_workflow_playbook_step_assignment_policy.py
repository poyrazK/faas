from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowPlaybookStepAssignmentPolicy")


@_attrs_define
class OperationWorkflowPlaybookStepAssignmentPolicy:
    """OperationWorkflowPlaybookStepAssignmentPolicy. Pinned team and completion window for one declared outcome follow-up
    step; coordination only.

    """

    label: str
    """OperationWorkflowPlaybookStepAssignmentPolicy.label. Exact unique declared follow-up label."""
    owner: str
    """OperationWorkflowPlaybookStepAssignmentPolicy.owner. Required trimmed control-free UTF-8 team label."""
    complete_after_seconds: int
    """OperationWorkflowPlaybookStepAssignmentPolicy.complete_after_seconds. Completion window from first list
    review; bounded to seven days."""

    def to_dict(self) -> dict[str, Any]:
        label = self.label

        owner = self.owner

        complete_after_seconds = self.complete_after_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "label": label,
                "owner": owner,
                "complete_after_seconds": complete_after_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        label = d.pop("label")

        owner = d.pop("owner")

        complete_after_seconds = d.pop("complete_after_seconds")

        operation_workflow_playbook_step_assignment_policy = cls(
            label=label,
            owner=owner,
            complete_after_seconds=complete_after_seconds,
        )

        return operation_workflow_playbook_step_assignment_policy
