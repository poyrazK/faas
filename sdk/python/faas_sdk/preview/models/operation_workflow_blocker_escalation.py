from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowBlockerEscalation")


@_attrs_define
class OperationWorkflowBlockerEscalation:
    """Read-only threshold finding evaluated at the attention cursor time against the current retained report and its
    pinned definition. Owner is the recommended escalation recipient, not the blocker assignee.

    """

    code: str
    operation: str
    owner: str
    after_seconds: int
    escalated_at: datetime.datetime
    """Time when the reported blocker first reached its declared threshold."""

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        operation = self.operation

        owner = self.owner

        after_seconds = self.after_seconds

        escalated_at = self.escalated_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "code": code,
                "operation": operation,
                "owner": owner,
                "after_seconds": after_seconds,
                "escalated_at": escalated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        operation = d.pop("operation")

        owner = d.pop("owner")

        after_seconds = d.pop("after_seconds")

        escalated_at = datetime.datetime.fromisoformat(d.pop("escalated_at"))

        operation_workflow_blocker_escalation = cls(
            code=code,
            operation=operation,
            owner=owner,
            after_seconds=after_seconds,
            escalated_at=escalated_at,
        )

        return operation_workflow_blocker_escalation
