from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowInstanceMilestoneRef")


@_attrs_define
class OperationWorkflowInstanceMilestoneRef:
    """Identity and occurrence and publication times of a milestone observed for a declared workflow step."""

    id: UUID
    operation_id: UUID
    occurred_at: datetime.datetime
    published_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        operation_id = str(self.operation_id)

        occurred_at = self.occurred_at.isoformat()

        published_at = self.published_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "operation_id": operation_id,
                "occurred_at": occurred_at,
                "published_at": published_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        operation_id = UUID(d.pop("operation_id"))

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        published_at = datetime.datetime.fromisoformat(d.pop("published_at"))

        operation_workflow_instance_milestone_ref = cls(
            id=id,
            operation_id=operation_id,
            occurred_at=occurred_at,
            published_at=published_at,
        )

        return operation_workflow_instance_milestone_ref
