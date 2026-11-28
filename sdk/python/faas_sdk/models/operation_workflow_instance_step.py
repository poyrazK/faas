from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_instance_milestone_ref import OperationWorkflowInstanceMilestoneRef


T = TypeVar("T", bound="OperationWorkflowInstanceStep")


@_attrs_define
class OperationWorkflowInstanceStep:
    """One declared step with both current-page facts and a retention-wide summary for the selected contract version.
    Retention fields include all matching facts still retained under the normal Operation retention rules.

    """

    step: str
    label: str
    milestone: str
    position: int
    observed: bool
    """True when a matching fact is visible on the current milestone page."""
    milestones_in_page: int
    """Number of matching facts on the current milestone page."""
    observed_in_retention: bool
    """True when at least one matching fact for the selected contract version remains under normal Operation
    retention. False means no matching fact is currently retained and does not prove it never occurred."""
    milestones_in_retention: int
    """Number of matching facts for the selected contract version that remain under normal Operation retention."""
    operation: str | Unset = UNSET
    operation_id: UUID | Unset = UNSET
    latest_milestone: OperationWorkflowInstanceMilestoneRef | Unset = UNSET
    """Identity and occurrence and publication times of a milestone observed for a declared workflow step."""
    latest_retained_milestone: OperationWorkflowInstanceMilestoneRef | Unset = UNSET
    """Identity and occurrence and publication times of a milestone observed for a declared workflow step."""

    def to_dict(self) -> dict[str, Any]:
        step = self.step

        label = self.label

        milestone = self.milestone

        position = self.position

        observed = self.observed

        milestones_in_page = self.milestones_in_page

        observed_in_retention = self.observed_in_retention

        milestones_in_retention = self.milestones_in_retention

        operation = self.operation

        operation_id: str | Unset = UNSET
        if not isinstance(self.operation_id, Unset):
            operation_id = str(self.operation_id)

        latest_milestone: dict[str, Any] | Unset = UNSET
        if not isinstance(self.latest_milestone, Unset):
            latest_milestone = self.latest_milestone.to_dict()

        latest_retained_milestone: dict[str, Any] | Unset = UNSET
        if not isinstance(self.latest_retained_milestone, Unset):
            latest_retained_milestone = self.latest_retained_milestone.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "step": step,
                "label": label,
                "milestone": milestone,
                "position": position,
                "observed": observed,
                "milestones_in_page": milestones_in_page,
                "observed_in_retention": observed_in_retention,
                "milestones_in_retention": milestones_in_retention,
            }
        )
        if operation is not UNSET:
            field_dict["operation"] = operation
        if operation_id is not UNSET:
            field_dict["operation_id"] = operation_id
        if latest_milestone is not UNSET:
            field_dict["latest_milestone"] = latest_milestone
        if latest_retained_milestone is not UNSET:
            field_dict["latest_retained_milestone"] = latest_retained_milestone

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_instance_milestone_ref import OperationWorkflowInstanceMilestoneRef

        d = dict(src_dict)
        step = d.pop("step")

        label = d.pop("label")

        milestone = d.pop("milestone")

        position = d.pop("position")

        observed = d.pop("observed")

        milestones_in_page = d.pop("milestones_in_page")

        observed_in_retention = d.pop("observed_in_retention")

        milestones_in_retention = d.pop("milestones_in_retention")

        operation = d.pop("operation", UNSET)

        _operation_id = d.pop("operation_id", UNSET)
        operation_id: UUID | Unset
        if isinstance(_operation_id, Unset):
            operation_id = UNSET
        else:
            operation_id = UUID(_operation_id)

        _latest_milestone = d.pop("latest_milestone", UNSET)
        latest_milestone: OperationWorkflowInstanceMilestoneRef | Unset
        if isinstance(_latest_milestone, Unset):
            latest_milestone = UNSET
        else:
            latest_milestone = OperationWorkflowInstanceMilestoneRef.from_dict(_latest_milestone)

        _latest_retained_milestone = d.pop("latest_retained_milestone", UNSET)
        latest_retained_milestone: OperationWorkflowInstanceMilestoneRef | Unset
        if isinstance(_latest_retained_milestone, Unset):
            latest_retained_milestone = UNSET
        else:
            latest_retained_milestone = OperationWorkflowInstanceMilestoneRef.from_dict(_latest_retained_milestone)

        operation_workflow_instance_step = cls(
            step=step,
            label=label,
            milestone=milestone,
            position=position,
            observed=observed,
            milestones_in_page=milestones_in_page,
            observed_in_retention=observed_in_retention,
            milestones_in_retention=milestones_in_retention,
            operation=operation,
            operation_id=operation_id,
            latest_milestone=latest_milestone,
            latest_retained_milestone=latest_retained_milestone,
        )

        return operation_workflow_instance_step
