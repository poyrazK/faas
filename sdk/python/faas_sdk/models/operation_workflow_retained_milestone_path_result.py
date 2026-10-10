from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_retained_milestone import OperationWorkflowRetainedMilestone


T = TypeVar("T", bound="OperationWorkflowRetainedMilestonePathResult")


@_attrs_define
class OperationWorkflowRetainedMilestonePathResult:
    """Complete evidence findings for one named alternative path. Satisfied means every requirement is verified at the
    shared evaluation time; passing facts from separate paths cannot combine to satisfy a group.

    """

    id: str
    satisfied: bool
    requirements: list[OperationWorkflowRetainedMilestone]

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        satisfied = self.satisfied

        requirements = []
        for requirements_item_data in self.requirements:
            requirements_item = requirements_item_data.to_dict()
            requirements.append(requirements_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "satisfied": satisfied,
                "requirements": requirements,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_retained_milestone import OperationWorkflowRetainedMilestone

        d = dict(src_dict)
        id = d.pop("id")

        satisfied = d.pop("satisfied")

        requirements = []
        _requirements = d.pop("requirements")
        for requirements_item_data in _requirements:
            requirements_item = OperationWorkflowRetainedMilestone.from_dict(requirements_item_data)

            requirements.append(requirements_item)

        operation_workflow_retained_milestone_path_result = cls(
            id=id,
            satisfied=satisfied,
            requirements=requirements,
        )

        return operation_workflow_retained_milestone_path_result
