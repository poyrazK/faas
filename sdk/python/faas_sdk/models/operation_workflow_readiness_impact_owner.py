from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowReadinessImpactOwner")


@_attrs_define
class OperationWorkflowReadinessImpactOwner:
    """OperationWorkflowReadinessImpactOwner. Effective queue ownership within a retained business impact group."""

    owner: str
    """OperationWorkflowReadinessImpactOwner.owner. Current inbox owner label; empty means unassigned."""
    task_count: int
    """OperationWorkflowReadinessImpactOwner.task_count. Matching retained inbox delivery tasks for this owner."""
    overdue_count: int
    """OperationWorkflowReadinessImpactOwner.overdue_count. Matching tasks with overdue queue attention."""

    def to_dict(self) -> dict[str, Any]:
        owner = self.owner

        task_count = self.task_count

        overdue_count = self.overdue_count

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "owner": owner,
                "task_count": task_count,
                "overdue_count": overdue_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        owner = d.pop("owner")

        task_count = d.pop("task_count")

        overdue_count = d.pop("overdue_count")

        operation_workflow_readiness_impact_owner = cls(
            owner=owner,
            task_count=task_count,
            overdue_count=overdue_count,
        )

        return operation_workflow_readiness_impact_owner
