from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_coordination_request_status import (
    OperationWorkflowReadinessCoordinationRequestStatus,
    check_operation_workflow_readiness_coordination_request_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowReadinessCoordinationRequest")


@_attrs_define
class OperationWorkflowReadinessCoordinationRequest:
    """OperationWorkflowReadinessCoordinationRequest. Shared inbox coordination with optimistic revision checks; omission
    of a follow-up time clears it.

    """

    revision: int
    """OperationWorkflowReadinessCoordinationRequest.revision. Shared coordination value; does not alter
    application evidence."""
    status: OperationWorkflowReadinessCoordinationRequestStatus
    """OperationWorkflowReadinessCoordinationRequest.status. Shared coordination value; does not alter application
    evidence."""
    owner: str
    """OperationWorkflowReadinessCoordinationRequest.owner. Shared coordination value; does not alter application
    evidence."""
    note: str
    """OperationWorkflowReadinessCoordinationRequest.note. Shared coordination value; does not alter application
    evidence."""
    follow_up_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReadinessCoordinationRequest.follow_up_at. Shared coordination value; does not alter
    application evidence."""

    def to_dict(self) -> dict[str, Any]:
        revision = self.revision

        status: str = self.status

        owner = self.owner

        note = self.note

        follow_up_at: str | Unset = UNSET
        if not isinstance(self.follow_up_at, Unset):
            follow_up_at = self.follow_up_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "revision": revision,
                "status": status,
                "owner": owner,
                "note": note,
            }
        )
        if follow_up_at is not UNSET:
            field_dict["follow_up_at"] = follow_up_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        revision = d.pop("revision")

        status = check_operation_workflow_readiness_coordination_request_status(d.pop("status"))

        owner = d.pop("owner")

        note = d.pop("note")

        _follow_up_at = d.pop("follow_up_at", UNSET)
        follow_up_at: datetime.datetime | Unset
        if isinstance(_follow_up_at, Unset):
            follow_up_at = UNSET
        else:
            follow_up_at = datetime.datetime.fromisoformat(_follow_up_at)

        operation_workflow_readiness_coordination_request = cls(
            revision=revision,
            status=status,
            owner=owner,
            note=note,
            follow_up_at=follow_up_at,
        )

        return operation_workflow_readiness_coordination_request
