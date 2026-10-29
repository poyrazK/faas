from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_retained_milestone_path import OperationWorkflowRetainedMilestonePath
    from ..models.operation_workflow_retained_milestone_requirement import OperationWorkflowRetainedMilestoneRequirement


T = TypeVar("T", bound="OperationWorkflowRetainedMilestoneGroup")


@_attrs_define
class OperationWorkflowRetainedMilestoneGroup:
    """Named alternatives in a pinned workflow edge. Declare either a nonempty any_of list or nonempty named paths. Any
    verified fact satisfies a flat group; every fact in one complete path satisfies a path group. All groups and
    mandatory prerequisites must pass. Across named paths a group permits at most 16 fact occurrences including repeated
    selectors. Changes require a new contract version.

    """

    id: str
    any_of: list[OperationWorkflowRetainedMilestoneRequirement] | Unset = UNSET
    paths: list[OperationWorkflowRetainedMilestonePath] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        any_of: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.any_of, Unset):
            any_of = []
            for any_of_item_data in self.any_of:
                any_of_item = any_of_item_data.to_dict()
                any_of.append(any_of_item)

        paths: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.paths, Unset):
            paths = []
            for paths_item_data in self.paths:
                paths_item = paths_item_data.to_dict()
                paths.append(paths_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
            }
        )
        if any_of is not UNSET:
            field_dict["any_of"] = any_of
        if paths is not UNSET:
            field_dict["paths"] = paths

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_retained_milestone_path import OperationWorkflowRetainedMilestonePath
        from ..models.operation_workflow_retained_milestone_requirement import (
            OperationWorkflowRetainedMilestoneRequirement,
        )

        d = dict(src_dict)
        id = d.pop("id")

        _any_of = d.pop("any_of", UNSET)
        any_of: list[OperationWorkflowRetainedMilestoneRequirement] | Unset = UNSET
        if _any_of is not UNSET:
            any_of = []
            for any_of_item_data in _any_of:
                any_of_item = OperationWorkflowRetainedMilestoneRequirement.from_dict(any_of_item_data)

                any_of.append(any_of_item)

        _paths = d.pop("paths", UNSET)
        paths: list[OperationWorkflowRetainedMilestonePath] | Unset = UNSET
        if _paths is not UNSET:
            paths = []
            for paths_item_data in _paths:
                paths_item = OperationWorkflowRetainedMilestonePath.from_dict(paths_item_data)

                paths.append(paths_item)

        operation_workflow_retained_milestone_group = cls(
            id=id,
            any_of=any_of,
            paths=paths,
        )

        return operation_workflow_retained_milestone_group
