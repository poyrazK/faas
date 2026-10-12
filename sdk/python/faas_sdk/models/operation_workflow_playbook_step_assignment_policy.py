from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

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
    acknowledge_after_seconds: int | Unset = UNSET
    """OperationWorkflowPlaybookStepAssignmentPolicy.acknowledge_after_seconds. Optional response window from queue
    delivery; zero disables. Separate from the original step completion deadline."""
    fallback_owner: str | Unset = UNSET
    """OperationWorkflowPlaybookStepAssignmentPolicy.fallback_owner. Optional distinct fallback team; requires a
    positive response window. Assignment changes preserve human ownership history."""

    def to_dict(self) -> dict[str, Any]:
        label = self.label

        owner = self.owner

        complete_after_seconds = self.complete_after_seconds

        acknowledge_after_seconds = self.acknowledge_after_seconds

        fallback_owner = self.fallback_owner

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "label": label,
                "owner": owner,
                "complete_after_seconds": complete_after_seconds,
            }
        )
        if acknowledge_after_seconds is not UNSET:
            field_dict["acknowledge_after_seconds"] = acknowledge_after_seconds
        if fallback_owner is not UNSET:
            field_dict["fallback_owner"] = fallback_owner

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        label = d.pop("label")

        owner = d.pop("owner")

        complete_after_seconds = d.pop("complete_after_seconds")

        acknowledge_after_seconds = d.pop("acknowledge_after_seconds", UNSET)

        fallback_owner = d.pop("fallback_owner", UNSET)

        operation_workflow_playbook_step_assignment_policy = cls(
            label=label,
            owner=owner,
            complete_after_seconds=complete_after_seconds,
            acknowledge_after_seconds=acknowledge_after_seconds,
            fallback_owner=fallback_owner,
        )

        return operation_workflow_playbook_step_assignment_policy
