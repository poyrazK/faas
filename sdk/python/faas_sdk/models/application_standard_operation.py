from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.application_standard_operation_state import (
    ApplicationStandardOperationState,
    check_application_standard_operation_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.application_standard_operation_target import ApplicationStandardOperationTarget


T = TypeVar("T", bound="ApplicationStandardOperation")


@_attrs_define
class ApplicationStandardOperation:
    """Saved operation progress. Persisted targets still require consumer observation; this response never advances it."""

    id: UUID
    org_id: UUID
    plan_id: UUID
    assignment_id: UUID
    approval_hash: str
    approved_by: UUID
    batch_size: int
    state: ApplicationStandardOperationState
    targets: list[ApplicationStandardOperationTarget]
    created_at: datetime.datetime
    updated_at: datetime.datetime
    error_code: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        org_id = str(self.org_id)

        plan_id = str(self.plan_id)

        assignment_id = str(self.assignment_id)

        approval_hash = self.approval_hash

        approved_by = str(self.approved_by)

        batch_size = self.batch_size

        state: str = self.state

        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        error_code = self.error_code

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "org_id": org_id,
                "plan_id": plan_id,
                "assignment_id": assignment_id,
                "approval_hash": approval_hash,
                "approved_by": approved_by,
                "batch_size": batch_size,
                "state": state,
                "targets": targets,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if error_code is not UNSET:
            field_dict["error_code"] = error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.application_standard_operation_target import ApplicationStandardOperationTarget

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        org_id = UUID(d.pop("org_id"))

        plan_id = UUID(d.pop("plan_id"))

        assignment_id = UUID(d.pop("assignment_id"))

        approval_hash = d.pop("approval_hash")

        approved_by = UUID(d.pop("approved_by"))

        batch_size = d.pop("batch_size")

        state = check_application_standard_operation_state(d.pop("state"))

        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = ApplicationStandardOperationTarget.from_dict(targets_item_data)

            targets.append(targets_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        error_code = d.pop("error_code", UNSET)

        application_standard_operation = cls(
            id=id,
            org_id=org_id,
            plan_id=plan_id,
            assignment_id=assignment_id,
            approval_hash=approval_hash,
            approved_by=approved_by,
            batch_size=batch_size,
            state=state,
            targets=targets,
            created_at=created_at,
            updated_at=updated_at,
            error_code=error_code,
        )

        return application_standard_operation
