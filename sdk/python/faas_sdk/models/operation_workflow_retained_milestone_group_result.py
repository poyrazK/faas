from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_retained_milestone import OperationWorkflowRetainedMilestone
    from ..models.operation_workflow_retained_milestone_path_result import OperationWorkflowRetainedMilestonePathResult


T = TypeVar("T", bound="OperationWorkflowRetainedMilestoneGroupResult")


@_attrs_define
class OperationWorkflowRetainedMilestoneGroupResult:
    """Evaluation of one retained alternative group. Flat groups return alternatives; compound groups return paths. Every
    fact includes freshness and invalidation and recovery diagnostics. A satisfied group requires at least one verified
    alternative or fully satisfied path.

    """

    id: str
    satisfied: bool
    selected_path_id: str | Unset = UNSET
    """When supplied by the application readiness request only this path determines group satisfaction; other paths
    remain diagnostic."""
    alternatives: list[OperationWorkflowRetainedMilestone] | Unset = UNSET
    paths: list[OperationWorkflowRetainedMilestonePathResult] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        satisfied = self.satisfied

        selected_path_id = self.selected_path_id

        alternatives: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.alternatives, Unset):
            alternatives = []
            for alternatives_item_data in self.alternatives:
                alternatives_item = alternatives_item_data.to_dict()
                alternatives.append(alternatives_item)

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
                "satisfied": satisfied,
            }
        )
        if selected_path_id is not UNSET:
            field_dict["selected_path_id"] = selected_path_id
        if alternatives is not UNSET:
            field_dict["alternatives"] = alternatives
        if paths is not UNSET:
            field_dict["paths"] = paths

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_retained_milestone import OperationWorkflowRetainedMilestone
        from ..models.operation_workflow_retained_milestone_path_result import (
            OperationWorkflowRetainedMilestonePathResult,
        )

        d = dict(src_dict)
        id = d.pop("id")

        satisfied = d.pop("satisfied")

        selected_path_id = d.pop("selected_path_id", UNSET)

        _alternatives = d.pop("alternatives", UNSET)
        alternatives: list[OperationWorkflowRetainedMilestone] | Unset = UNSET
        if _alternatives is not UNSET:
            alternatives = []
            for alternatives_item_data in _alternatives:
                alternatives_item = OperationWorkflowRetainedMilestone.from_dict(alternatives_item_data)

                alternatives.append(alternatives_item)

        _paths = d.pop("paths", UNSET)
        paths: list[OperationWorkflowRetainedMilestonePathResult] | Unset = UNSET
        if _paths is not UNSET:
            paths = []
            for paths_item_data in _paths:
                paths_item = OperationWorkflowRetainedMilestonePathResult.from_dict(paths_item_data)

                paths.append(paths_item)

        operation_workflow_retained_milestone_group_result = cls(
            id=id,
            satisfied=satisfied,
            selected_path_id=selected_path_id,
            alternatives=alternatives,
            paths=paths,
        )

        return operation_workflow_retained_milestone_group_result
