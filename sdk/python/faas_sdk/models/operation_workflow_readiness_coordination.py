from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_readiness_coordination_status import (
    OperationWorkflowReadinessCoordinationStatus,
    check_operation_workflow_readiness_coordination_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_workflow_readiness_completion import OperationWorkflowReadinessCompletion


T = TypeVar("T", bound="OperationWorkflowReadinessCoordination")


@_attrs_define
class OperationWorkflowReadinessCoordination:
    """OperationWorkflowReadinessCoordination. Shared inbox coordination with optimistic revision checks; omission of a
    follow-up time clears it.

    """

    revision: int
    """OperationWorkflowReadinessCoordination.revision. Shared coordination value; does not alter application
    evidence."""
    status: OperationWorkflowReadinessCoordinationStatus
    """OperationWorkflowReadinessCoordination.status. Shared coordination value; does not alter application
    evidence."""
    owner: str
    """OperationWorkflowReadinessCoordination.owner. Shared coordination value; does not alter application
    evidence."""
    note: str
    """OperationWorkflowReadinessCoordination.note. Shared coordination value; does not alter application evidence."""
    updated_at: datetime.datetime
    """OperationWorkflowReadinessCoordination.updated_at. Shared coordination value; does not alter application
    evidence."""
    updated_by: str
    """OperationWorkflowReadinessCoordination.updated_by. Shared coordination value; does not alter application
    evidence."""
    completion: OperationWorkflowReadinessCompletion | Unset = UNSET
    """Immutable recorded follow-up completion with server-derived actor, write time and advisory pre-write
    evidence check. Retained with the original notification."""
    follow_up_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReadinessCoordination.follow_up_at. Shared coordination value; does not alter application
    evidence."""
    acknowledged_at: datetime.datetime | Unset = UNSET
    """OperationWorkflowReadinessCoordination.acknowledged_at. Shared coordination value; does not alter
    application evidence."""
    acknowledged_by: str | Unset = UNSET
    """OperationWorkflowReadinessCoordination.acknowledged_by. Shared coordination value; does not alter
    application evidence."""

    def to_dict(self) -> dict[str, Any]:
        revision = self.revision

        status: str = self.status

        owner = self.owner

        note = self.note

        updated_at = self.updated_at.isoformat()

        updated_by = self.updated_by

        completion: dict[str, Any] | Unset = UNSET
        if not isinstance(self.completion, Unset):
            completion = self.completion.to_dict()

        follow_up_at: str | Unset = UNSET
        if not isinstance(self.follow_up_at, Unset):
            follow_up_at = self.follow_up_at.isoformat()

        acknowledged_at: str | Unset = UNSET
        if not isinstance(self.acknowledged_at, Unset):
            acknowledged_at = self.acknowledged_at.isoformat()

        acknowledged_by = self.acknowledged_by

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "revision": revision,
                "status": status,
                "owner": owner,
                "note": note,
                "updated_at": updated_at,
                "updated_by": updated_by,
            }
        )
        if completion is not UNSET:
            field_dict["completion"] = completion
        if follow_up_at is not UNSET:
            field_dict["follow_up_at"] = follow_up_at
        if acknowledged_at is not UNSET:
            field_dict["acknowledged_at"] = acknowledged_at
        if acknowledged_by is not UNSET:
            field_dict["acknowledged_by"] = acknowledged_by

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_workflow_readiness_completion import OperationWorkflowReadinessCompletion

        d = dict(src_dict)
        revision = d.pop("revision")

        status = check_operation_workflow_readiness_coordination_status(d.pop("status"))

        owner = d.pop("owner")

        note = d.pop("note")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        updated_by = d.pop("updated_by")

        _completion = d.pop("completion", UNSET)
        completion: OperationWorkflowReadinessCompletion | Unset
        if isinstance(_completion, Unset):
            completion = UNSET
        else:
            completion = OperationWorkflowReadinessCompletion.from_dict(_completion)

        _follow_up_at = d.pop("follow_up_at", UNSET)
        follow_up_at: datetime.datetime | Unset
        if isinstance(_follow_up_at, Unset):
            follow_up_at = UNSET
        else:
            follow_up_at = datetime.datetime.fromisoformat(_follow_up_at)

        _acknowledged_at = d.pop("acknowledged_at", UNSET)
        acknowledged_at: datetime.datetime | Unset
        if isinstance(_acknowledged_at, Unset):
            acknowledged_at = UNSET
        else:
            acknowledged_at = datetime.datetime.fromisoformat(_acknowledged_at)

        acknowledged_by = d.pop("acknowledged_by", UNSET)

        operation_workflow_readiness_coordination = cls(
            revision=revision,
            status=status,
            owner=owner,
            note=note,
            updated_at=updated_at,
            updated_by=updated_by,
            completion=completion,
            follow_up_at=follow_up_at,
            acknowledged_at=acknowledged_at,
            acknowledged_by=acknowledged_by,
        )

        return operation_workflow_readiness_coordination
