from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_attention_summary_group_by import (
    OperationWorkflowAttentionSummaryGroupBy,
    check_operation_workflow_attention_summary_group_by,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_attention_group import OperationWorkflowAttentionGroup
    from ..models.operation_workflow_attention_stats import OperationWorkflowAttentionStats


T = TypeVar("T", bound="OperationWorkflowAttentionSummary")


@_attrs_define
class OperationWorkflowAttentionSummary:
    group_by: OperationWorkflowAttentionSummaryGroupBy
    evaluated_at: datetime.datetime
    totals: OperationWorkflowAttentionStats
    groups: list[OperationWorkflowAttentionGroup]
    next_cursor: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        group_by: str = self.group_by

        evaluated_at = self.evaluated_at.isoformat()

        totals = self.totals.to_dict()

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
                "totals": totals,
                "groups": groups,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_attention_group import OperationWorkflowAttentionGroup
        from ..models.operation_workflow_attention_stats import OperationWorkflowAttentionStats

        d = dict(src_dict)
        group_by = check_operation_workflow_attention_summary_group_by(d.pop("group_by"))

        evaluated_at = datetime.datetime.fromisoformat(d.pop("evaluated_at"))

        totals = OperationWorkflowAttentionStats.from_dict(d.pop("totals"))

        groups = []
        _groups = d.pop("groups")
        for groups_item_data in _groups:
            groups_item = OperationWorkflowAttentionGroup.from_dict(groups_item_data)

            groups.append(groups_item)

        next_cursor = d.pop("next_cursor", UNSET)

        operation_workflow_attention_summary = cls(
            group_by=group_by,
            evaluated_at=evaluated_at,
            totals=totals,
            groups=groups,
            next_cursor=next_cursor,
        )

        return operation_workflow_attention_summary
