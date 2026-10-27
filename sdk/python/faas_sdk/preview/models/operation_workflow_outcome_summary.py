from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_outcome_summary_group_by import (
    OperationWorkflowOutcomeSummaryGroupBy,
    check_operation_workflow_outcome_summary_group_by,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_outcome_group import OperationWorkflowOutcomeGroup


T = TypeVar("T", bound="OperationWorkflowOutcomeSummary")


@_attrs_define
class OperationWorkflowOutcomeSummary:
    """Total matching terminal instances and a bounded page of outcome aggregation groups."""

    group_by: OperationWorkflowOutcomeSummaryGroupBy
    evaluated_at: datetime.datetime
    workflow_count: int
    groups: list[OperationWorkflowOutcomeGroup]
    next_cursor: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        group_by: str = self.group_by

        evaluated_at = self.evaluated_at.isoformat()

        workflow_count = self.workflow_count

        groups = []
        for groups_item_data in self.groups:
            groups_item = groups_item_data.to_dict()
            groups.append(groups_item)

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "group_by": group_by,
                "evaluated_at": evaluated_at,
                "workflow_count": workflow_count,
                "groups": groups,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_outcome_group import OperationWorkflowOutcomeGroup

        d = dict(src_dict)
        group_by = check_operation_workflow_outcome_summary_group_by(d.pop("group_by"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        workflow_count = d.pop("workflow_count")

        groups = []
        _groups = d.pop("groups")
        for groups_item_data in _groups:
            groups_item = OperationWorkflowOutcomeGroup.from_dict(groups_item_data)

            groups.append(groups_item)

        next_cursor = d.pop("next_cursor", UNSET)

        operation_workflow_outcome_summary = cls(
            group_by=group_by,
            evaluated_at=evaluated_at,
            workflow_count=workflow_count,
            groups=groups,
            next_cursor=next_cursor,
        )

        return operation_workflow_outcome_summary
