from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_gap_queue_guidance import OperationWorkflowGapQueueGuidance
    from ..models.operation_workflow_new_missing_condition_progress_count import (
        OperationWorkflowNewMissingConditionProgressCount,
    )


T = TypeVar("T", bound="OperationWorkflowGapQueuePriority")


@_attrs_define
class OperationWorkflowGapQueuePriority:
    """All current reporting gaps before queue filters and evidence previews. Guidance contains every eligible pinned
    missing expectation, including owners beyond the evidence preview; it carries no prior report facts. Blocked
    intervals spanning contracts are omitted.

    """

    missing_condition_count: int
    statuses: list[OperationWorkflowNewMissingConditionProgressCount]
    refresh_owners: list[str]
    unassigned_condition_count: int
    guidance: list[OperationWorkflowGapQueueGuidance]
    blocked_seconds: int
    explanation: str

    def to_dict(self) -> dict[str, Any]:
        missing_condition_count = self.missing_condition_count

        statuses = []
        for statuses_item_data in self.statuses:
            statuses_item = statuses_item_data.to_dict()
            statuses.append(statuses_item)

        refresh_owners = self.refresh_owners

        unassigned_condition_count = self.unassigned_condition_count

        guidance = []
        for guidance_item_data in self.guidance:
            guidance_item = guidance_item_data.to_dict()
            guidance.append(guidance_item)

        blocked_seconds = self.blocked_seconds

        explanation = self.explanation

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "missing_condition_count": missing_condition_count,
                "statuses": statuses,
                "refresh_owners": refresh_owners,
                "unassigned_condition_count": unassigned_condition_count,
                "guidance": guidance,
                "blocked_seconds": blocked_seconds,
                "explanation": explanation,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_gap_queue_guidance import OperationWorkflowGapQueueGuidance
        from ..models.operation_workflow_new_missing_condition_progress_count import (
            OperationWorkflowNewMissingConditionProgressCount,
        )

        d = dict(src_dict)
        missing_condition_count = d.pop("missing_condition_count")

        statuses = []
        _statuses = d.pop("statuses")
        for statuses_item_data in _statuses:
            statuses_item = OperationWorkflowNewMissingConditionProgressCount.from_dict(statuses_item_data)

            statuses.append(statuses_item)

        refresh_owners = cast(list[str], d.pop("refresh_owners"))

        unassigned_condition_count = d.pop("unassigned_condition_count")

        guidance = []
        _guidance = d.pop("guidance")
        for guidance_item_data in _guidance:
            guidance_item = OperationWorkflowGapQueueGuidance.from_dict(guidance_item_data)

            guidance.append(guidance_item)

        blocked_seconds = d.pop("blocked_seconds")

        explanation = d.pop("explanation")

        operation_workflow_gap_queue_priority = cls(
            missing_condition_count=missing_condition_count,
            statuses=statuses,
            refresh_owners=refresh_owners,
            unassigned_condition_count=unassigned_condition_count,
            guidance=guidance,
            blocked_seconds=blocked_seconds,
            explanation=explanation,
        )

        return operation_workflow_gap_queue_priority
