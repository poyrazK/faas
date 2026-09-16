from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_promotion_status_response_rollback_status import (
    ProjectEnvironmentPromotionStatusResponseRollbackStatus,
    check_project_environment_promotion_status_response_rollback_status,
)
from ..models.project_environment_promotion_status_response_status import (
    ProjectEnvironmentPromotionStatusResponseStatus,
    check_project_environment_promotion_status_response_status,
)
from ..models.project_environment_promotion_status_response_verification_status import (
    ProjectEnvironmentPromotionStatusResponseVerificationStatus,
    check_project_environment_promotion_status_response_verification_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_environment_promotion_status_workload_response import (
        ProjectEnvironmentPromotionStatusWorkloadResponse,
    )


T = TypeVar("T", bound="ProjectEnvironmentPromotionStatusResponse")


@_attrs_define
class ProjectEnvironmentPromotionStatusResponse:
    """Durable status for a project environment promotion operation."""

    promotion_id: str
    project_slug: str
    from_environment: str
    to_environment: str
    promotion_hash: str
    status: ProjectEnvironmentPromotionStatusResponseStatus
    created_at: datetime.datetime
    updated_at: datetime.datetime
    workloads: list[ProjectEnvironmentPromotionStatusWorkloadResponse]
    error: str | Unset = UNSET
    completed_at: datetime.datetime | Unset = UNSET
    rollback_status: ProjectEnvironmentPromotionStatusResponseRollbackStatus | Unset = UNSET
    rollback_error: str | Unset = UNSET
    rollback_started_at: datetime.datetime | Unset = UNSET
    rollback_completed_at: datetime.datetime | Unset = UNSET
    verification_status: ProjectEnvironmentPromotionStatusResponseVerificationStatus | Unset = UNSET
    verification_error: str | Unset = UNSET
    verification_started_at: datetime.datetime | Unset = UNSET
    verification_completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        promotion_id = self.promotion_id

        project_slug = self.project_slug

        from_environment = self.from_environment

        to_environment = self.to_environment

        promotion_hash = self.promotion_hash

        status: str = self.status

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        workloads = []
        for workloads_item_data in self.workloads:
            workloads_item = workloads_item_data.to_dict()
            workloads.append(workloads_item)

        error = self.error

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        rollback_status: str | Unset = UNSET
        if not isinstance(self.rollback_status, Unset):
            rollback_status = self.rollback_status

        rollback_error = self.rollback_error

        rollback_started_at: str | Unset = UNSET
        if not isinstance(self.rollback_started_at, Unset):
            rollback_started_at = self.rollback_started_at.isoformat()

        rollback_completed_at: str | Unset = UNSET
        if not isinstance(self.rollback_completed_at, Unset):
            rollback_completed_at = self.rollback_completed_at.isoformat()

        verification_status: str | Unset = UNSET
        if not isinstance(self.verification_status, Unset):
            verification_status = self.verification_status

        verification_error = self.verification_error

        verification_started_at: str | Unset = UNSET
        if not isinstance(self.verification_started_at, Unset):
            verification_started_at = self.verification_started_at.isoformat()

        verification_completed_at: str | Unset = UNSET
        if not isinstance(self.verification_completed_at, Unset):
            verification_completed_at = self.verification_completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "promotion_id": promotion_id,
                "project_slug": project_slug,
                "from_environment": from_environment,
                "to_environment": to_environment,
                "promotion_hash": promotion_hash,
                "status": status,
                "created_at": created_at,
                "updated_at": updated_at,
                "workloads": workloads,
            }
        )
        if error is not UNSET:
            field_dict["error"] = error
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at
        if rollback_status is not UNSET:
            field_dict["rollback_status"] = rollback_status
        if rollback_error is not UNSET:
            field_dict["rollback_error"] = rollback_error
        if rollback_started_at is not UNSET:
            field_dict["rollback_started_at"] = rollback_started_at
        if rollback_completed_at is not UNSET:
            field_dict["rollback_completed_at"] = rollback_completed_at
        if verification_status is not UNSET:
            field_dict["verification_status"] = verification_status
        if verification_error is not UNSET:
            field_dict["verification_error"] = verification_error
        if verification_started_at is not UNSET:
            field_dict["verification_started_at"] = verification_started_at
        if verification_completed_at is not UNSET:
            field_dict["verification_completed_at"] = verification_completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_promotion_status_workload_response import (
            ProjectEnvironmentPromotionStatusWorkloadResponse,
        )

        d = dict(src_dict)
        promotion_id = d.pop("promotion_id")

        project_slug = d.pop("project_slug")

        from_environment = d.pop("from_environment")

        to_environment = d.pop("to_environment")

        promotion_hash = d.pop("promotion_hash")

        status = check_project_environment_promotion_status_response_status(d.pop("status"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        workloads = []
        _workloads = d.pop("workloads")
        for workloads_item_data in _workloads:
            workloads_item = ProjectEnvironmentPromotionStatusWorkloadResponse.from_dict(workloads_item_data)

            workloads.append(workloads_item)

        error = d.pop("error", UNSET)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        _rollback_status = d.pop("rollback_status", UNSET)
        rollback_status: ProjectEnvironmentPromotionStatusResponseRollbackStatus | Unset
        if isinstance(_rollback_status, Unset):
            rollback_status = UNSET
        else:
            rollback_status = check_project_environment_promotion_status_response_rollback_status(_rollback_status)

        rollback_error = d.pop("rollback_error", UNSET)

        _rollback_started_at = d.pop("rollback_started_at", UNSET)
        rollback_started_at: datetime.datetime | Unset
        if isinstance(_rollback_started_at, Unset):
            rollback_started_at = UNSET
        else:
            rollback_started_at = datetime.datetime.fromisoformat(_rollback_started_at)

        _rollback_completed_at = d.pop("rollback_completed_at", UNSET)
        rollback_completed_at: datetime.datetime | Unset
        if isinstance(_rollback_completed_at, Unset):
            rollback_completed_at = UNSET
        else:
            rollback_completed_at = datetime.datetime.fromisoformat(_rollback_completed_at)

        _verification_status = d.pop("verification_status", UNSET)
        verification_status: ProjectEnvironmentPromotionStatusResponseVerificationStatus | Unset
        if isinstance(_verification_status, Unset):
            verification_status = UNSET
        else:
            verification_status = check_project_environment_promotion_status_response_verification_status(
                _verification_status
            )

        verification_error = d.pop("verification_error", UNSET)

        _verification_started_at = d.pop("verification_started_at", UNSET)
        verification_started_at: datetime.datetime | Unset
        if isinstance(_verification_started_at, Unset):
            verification_started_at = UNSET
        else:
            verification_started_at = datetime.datetime.fromisoformat(_verification_started_at)

        _verification_completed_at = d.pop("verification_completed_at", UNSET)
        verification_completed_at: datetime.datetime | Unset
        if isinstance(_verification_completed_at, Unset):
            verification_completed_at = UNSET
        else:
            verification_completed_at = datetime.datetime.fromisoformat(_verification_completed_at)

        project_environment_promotion_status_response = cls(
            promotion_id=promotion_id,
            project_slug=project_slug,
            from_environment=from_environment,
            to_environment=to_environment,
            promotion_hash=promotion_hash,
            status=status,
            created_at=created_at,
            updated_at=updated_at,
            workloads=workloads,
            error=error,
            completed_at=completed_at,
            rollback_status=rollback_status,
            rollback_error=rollback_error,
            rollback_started_at=rollback_started_at,
            rollback_completed_at=rollback_completed_at,
            verification_status=verification_status,
            verification_error=verification_error,
            verification_started_at=verification_started_at,
            verification_completed_at=verification_completed_at,
        )

        project_environment_promotion_status_response.additional_properties = d
        return project_environment_promotion_status_response

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
