from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_retained_milestone_requirement import OperationWorkflowRetainedMilestoneRequirement


T = TypeVar("T", bound="OperationWorkflowRetainedMilestoneGroup")


@_attrs_define
class OperationWorkflowRetainedMilestoneGroup:
    """Named alternatives in a pinned workflow edge. Any one verified retained fact satisfies this group; all groups and
    mandatory prerequisites must pass. Group IDs and selectors are canonicalized and require a new contract version when
    changed.

    """

    id: str
    any_of: list[OperationWorkflowRetainedMilestoneRequirement]

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        any_of = []
        for any_of_item_data in self.any_of:
            any_of_item = any_of_item_data.to_dict()
            any_of.append(any_of_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "any_of": any_of,
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

        any_of = []
        _any_of = d.pop("any_of")
        for any_of_item_data in _any_of:
            any_of_item = OperationWorkflowRetainedMilestoneRequirement.from_dict(any_of_item_data)

            any_of.append(any_of_item)

        operation_workflow_retained_milestone_group = cls(
            id=id,
            any_of=any_of,
        )

        return operation_workflow_retained_milestone_group
