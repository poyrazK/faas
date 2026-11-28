from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.operation_workflow_attention_stats import OperationWorkflowAttentionStats


T = TypeVar("T", bound="OperationWorkflowAttentionGroup")


@_attrs_define
class OperationWorkflowAttentionGroup:
    """Attention statistics for one value of the selected grouping dimension. Owner grouping uses an empty value for
    unassigned blockers; owner counts and ages cover that owner only.

    """

    value: str
    stats: OperationWorkflowAttentionStats
    """Aggregate counts and age measurements for workflows, blockers, deadlines, and unresolved dependencies."""

    def to_dict(self) -> dict[str, Any]:
        value = self.value

        stats = self.stats.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "value": value,
                "stats": stats,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_attention_stats import OperationWorkflowAttentionStats

        d = dict(src_dict)
        value = d.pop("value")

        stats = OperationWorkflowAttentionStats.from_dict(d.pop("stats"))

        operation_workflow_attention_group = cls(
            value=value,
            stats=stats,
        )

        return operation_workflow_attention_group
