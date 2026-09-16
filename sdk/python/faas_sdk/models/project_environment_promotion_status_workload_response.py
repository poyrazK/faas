from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_promotion_status_workload_response_rollback_status import (
    ProjectEnvironmentPromotionStatusWorkloadResponseRollbackStatus,
    check_project_environment_promotion_status_workload_response_rollback_status,
)
from ..models.project_environment_promotion_status_workload_response_status import (
    ProjectEnvironmentPromotionStatusWorkloadResponseStatus,
    check_project_environment_promotion_status_workload_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectEnvironmentPromotionStatusWorkloadResponse")


@_attrs_define
class ProjectEnvironmentPromotionStatusWorkloadResponse:
    """Durable checkpoint for one workload in an environment promotion."""

    workload_slug: str
    workload_name: str
    status: ProjectEnvironmentPromotionStatusWorkloadResponseStatus
    source_deployment_id: str | Unset = UNSET
    previous_target_deployment_id: str | Unset = UNSET
    target_deployment_id: str | Unset = UNSET
    error: str | Unset = UNSET
    rollback_status: ProjectEnvironmentPromotionStatusWorkloadResponseRollbackStatus | Unset = UNSET
    restored_target_deployment_id: str | Unset = UNSET
    rollback_error: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workload_slug = self.workload_slug

        workload_name = self.workload_name

        status: str = self.status

        source_deployment_id = self.source_deployment_id

        previous_target_deployment_id = self.previous_target_deployment_id

        target_deployment_id = self.target_deployment_id

        error = self.error

        rollback_status: str | Unset = UNSET
        if not isinstance(self.rollback_status, Unset):
            rollback_status = self.rollback_status

        restored_target_deployment_id = self.restored_target_deployment_id

        rollback_error = self.rollback_error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workload_slug": workload_slug,
                "workload_name": workload_name,
                "status": status,
            }
        )
        if source_deployment_id is not UNSET:
            field_dict["source_deployment_id"] = source_deployment_id
        if previous_target_deployment_id is not UNSET:
            field_dict["previous_target_deployment_id"] = previous_target_deployment_id
        if target_deployment_id is not UNSET:
            field_dict["target_deployment_id"] = target_deployment_id
        if error is not UNSET:
            field_dict["error"] = error
        if rollback_status is not UNSET:
            field_dict["rollback_status"] = rollback_status
        if restored_target_deployment_id is not UNSET:
            field_dict["restored_target_deployment_id"] = restored_target_deployment_id
        if rollback_error is not UNSET:
            field_dict["rollback_error"] = rollback_error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workload_slug = d.pop("workload_slug")

        workload_name = d.pop("workload_name")

        status = check_project_environment_promotion_status_workload_response_status(d.pop("status"))

        source_deployment_id = d.pop("source_deployment_id", UNSET)

        previous_target_deployment_id = d.pop("previous_target_deployment_id", UNSET)

        target_deployment_id = d.pop("target_deployment_id", UNSET)

        error = d.pop("error", UNSET)

        _rollback_status = d.pop("rollback_status", UNSET)
        rollback_status: ProjectEnvironmentPromotionStatusWorkloadResponseRollbackStatus | Unset
        if isinstance(_rollback_status, Unset):
            rollback_status = UNSET
        else:
            rollback_status = check_project_environment_promotion_status_workload_response_rollback_status(
                _rollback_status
            )

        restored_target_deployment_id = d.pop("restored_target_deployment_id", UNSET)

        rollback_error = d.pop("rollback_error", UNSET)

        project_environment_promotion_status_workload_response = cls(
            workload_slug=workload_slug,
            workload_name=workload_name,
            status=status,
            source_deployment_id=source_deployment_id,
            previous_target_deployment_id=previous_target_deployment_id,
            target_deployment_id=target_deployment_id,
            error=error,
            rollback_status=rollback_status,
            restored_target_deployment_id=restored_target_deployment_id,
            rollback_error=rollback_error,
        )

        project_environment_promotion_status_workload_response.additional_properties = d
        return project_environment_promotion_status_workload_response

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
