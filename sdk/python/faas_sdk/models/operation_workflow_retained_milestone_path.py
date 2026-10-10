from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_retained_milestone_requirement import OperationWorkflowRetainedMilestoneRequirement


T = TypeVar("T", bound="OperationWorkflowRetainedMilestonePath")


@_attrs_define
class OperationWorkflowRetainedMilestonePath:
    """One uniquely named business path within an alternative group. Every declared retained fact must be verified together
    at the evaluation time; selectors are canonicalized and unique within the path.

    """

    id: str
    all_of: list[OperationWorkflowRetainedMilestoneRequirement]

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        all_of = []
        for all_of_item_data in self.all_of:
            all_of_item = all_of_item_data.to_dict()
            all_of.append(all_of_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "all_of": all_of,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_retained_milestone_requirement import (
            OperationWorkflowRetainedMilestoneRequirement,
        )

        d = dict(src_dict)
        id = d.pop("id")

        all_of = []
        _all_of = d.pop("all_of")
        for all_of_item_data in _all_of:
            all_of_item = OperationWorkflowRetainedMilestoneRequirement.from_dict(all_of_item_data)

            all_of.append(all_of_item)

        operation_workflow_retained_milestone_path = cls(
            id=id,
            all_of=all_of,
        )

        return operation_workflow_retained_milestone_path
