from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_review_response_change_status import (
    OperationWorkflowReviewResponseChangeStatus,
    check_operation_workflow_review_response_change_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReviewResponseChange")


@_attrs_define
class OperationWorkflowReviewResponseChange:
    """OperationWorkflowReviewResponseChange. Authenticated response update snapshot."""

    owner: str
    """OperationWorkflowReviewResponseChange.owner. Responsible team label; trimmed control-free UTF-8 limited to
    256 bytes."""
    plan: str
    """OperationWorkflowReviewResponseChange.plan. Human response plan; nonblank UTF-8 limited to 1024 bytes. Line
    breaks and tabs are allowed."""
    status: OperationWorkflowReviewResponseChangeStatus
    """OperationWorkflowReviewResponseChange.status. Active ownership or human resolution after a fresh scoped
    check of linked source reviews."""
    at: datetime.datetime
    """OperationWorkflowReviewResponseChange.at. Server change time."""
    actor: str
    """OperationWorkflowReviewResponseChange.actor. Authenticated response editor."""
    check_in_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReviewResponseChange.check_in_at. Required future check-in for active responses; absent
    when resolved. Replaces inbox attention time without changing the original alert response deadline."""

    def to_dict(self) -> dict[str, Any]:
        owner = self.owner

        plan = self.plan

        status: str = self.status

        at = self.at.isoformat()

        actor = self.actor

        check_in_at: str | Unset = UNSET
        if not isinstance(self.check_in_at, Unset):
            check_in_at = self.check_in_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "owner": owner,
                "plan": plan,
                "status": status,
                "at": at,
                "actor": actor,
            }
        )
        if check_in_at is not UNSET:
            field_dict["check_in_at"] = check_in_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        owner = d.pop("owner")

        plan = d.pop("plan")

        status = check_operation_workflow_review_response_change_status(d.pop("status"))

        at = datetime.datetime.fromisoformat(d.pop("at"))

        actor = d.pop("actor")

        _check_in_at = d.pop("check_in_at", UNSET)
        check_in_at: datetime.datetime | Unset
        if isinstance(_check_in_at, Unset):
            check_in_at = UNSET
        else:
            check_in_at = datetime.datetime.fromisoformat(_check_in_at)

        operation_workflow_review_response_change = cls(
            owner=owner,
            plan=plan,
            status=status,
            at=at,
            actor=actor,
            check_in_at=check_in_at,
        )

        return operation_workflow_review_response_change
