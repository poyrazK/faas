from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_retained_milestone import OperationWorkflowRetainedMilestone


T = TypeVar("T", bound="OperationWorkflowRetainedMilestoneGroupResult")


@_attrs_define
class OperationWorkflowRetainedMilestoneGroupResult:
    """Evaluation of one retained alternative group. Every alternative includes existing freshness and invalidation and
    recovery diagnostics; unusable alternatives do not deny a group with a verified alternative.

    """

    id: str
    satisfied: bool
    alternatives: list[OperationWorkflowRetainedMilestone]

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        satisfied = self.satisfied

        alternatives = []
        for alternatives_item_data in self.alternatives:
            alternatives_item = alternatives_item_data.to_dict()
            alternatives.append(alternatives_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "satisfied": satisfied,
                "alternatives": alternatives,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_retained_milestone import OperationWorkflowRetainedMilestone

        d = dict(src_dict)
        id = d.pop("id")

        satisfied = d.pop("satisfied")

        alternatives = []
        _alternatives = d.pop("alternatives")
        for alternatives_item_data in _alternatives:
            alternatives_item = OperationWorkflowRetainedMilestone.from_dict(alternatives_item_data)

            alternatives.append(alternatives_item)

        operation_workflow_retained_milestone_group_result = cls(
            id=id,
            satisfied=satisfied,
            alternatives=alternatives,
        )

        return operation_workflow_retained_milestone_group_result
