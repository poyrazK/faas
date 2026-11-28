from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowReadinessDueReminder")


@_attrs_define
class OperationWorkflowReadinessDueReminder:
    """One durable in-app reminder marker for the original notification and follow-up deadline; rescheduling or clearing
    retires it. Does not authorize execution.

    """

    id: UUID
    """OperationWorkflowReadinessDueReminder.id. Stable UUID derived from notification identity and normalized
    deadline, shared across retries and restarts."""
    due_at: datetime.datetime
    """OperationWorkflowReadinessDueReminder.due_at. Deadline this reminder belongs to."""
    created_at: datetime.datetime
    """OperationWorkflowReadinessDueReminder.created_at. Successful reminder publication time."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        due_at = self.due_at.isoformat()

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "due_at": due_at,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        due_at = datetime.datetime.fromisoformat(d.pop("due_at"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        operation_workflow_readiness_due_reminder = cls(
            id=id,
            due_at=due_at,
            created_at=created_at,
        )

        return operation_workflow_readiness_due_reminder
