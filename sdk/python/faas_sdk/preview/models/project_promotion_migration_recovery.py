from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_promotion_migration_recovery_status import (
    ProjectPromotionMigrationRecoveryStatus,
    check_project_promotion_migration_recovery_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_promotion_migration_recovery_request import ProjectPromotionMigrationRecoveryRequest


T = TypeVar("T", bound="ProjectPromotionMigrationRecovery")


@_attrs_define
class ProjectPromotionMigrationRecovery:
    """Durable acknowledgement and deadline for an operator's promotion migration recovery decision."""

    request: ProjectPromotionMigrationRecoveryRequest
    """Operator inspection evidence and captured task identity for resuming or resolving a promotion migration
    incident."""
    status: ProjectPromotionMigrationRecoveryStatus
    operator_account_id: UUID
    requested_at: datetime.datetime
    deadline: datetime.datetime
    reason: str | Unset = UNSET
    resolved_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request = self.request.to_dict()

        status: str = self.status

        operator_account_id = str(self.operator_account_id)

        requested_at = self.requested_at.isoformat()

        deadline = self.deadline.isoformat()

        reason = self.reason

        resolved_at: str | Unset = UNSET
        if not isinstance(self.resolved_at, Unset):
            resolved_at = self.resolved_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request": request,
                "status": status,
                "operator_account_id": operator_account_id,
                "requested_at": requested_at,
                "deadline": deadline,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if resolved_at is not UNSET:
            field_dict["resolved_at"] = resolved_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_promotion_migration_recovery_request import ProjectPromotionMigrationRecoveryRequest

        d = dict(src_dict)
        request = ProjectPromotionMigrationRecoveryRequest.from_dict(d.pop("request"))

        status = check_project_promotion_migration_recovery_status(d.pop("status"))

        operator_account_id = UUID(d.pop("operator_account_id"))

        requested_at = datetime.datetime.fromisoformat(d.pop("requested_at"))

        deadline = datetime.datetime.fromisoformat(d.pop("deadline"))

        reason = d.pop("reason", UNSET)

        _resolved_at = d.pop("resolved_at", UNSET)
        resolved_at: datetime.datetime | Unset
        if isinstance(_resolved_at, Unset):
            resolved_at = UNSET
        else:
            resolved_at = datetime.datetime.fromisoformat(_resolved_at)

        project_promotion_migration_recovery = cls(
            request=request,
            status=status,
            operator_account_id=operator_account_id,
            requested_at=requested_at,
            deadline=deadline,
            reason=reason,
            resolved_at=resolved_at,
        )

        project_promotion_migration_recovery.additional_properties = d
        return project_promotion_migration_recovery

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
