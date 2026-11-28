from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.project_environment_qualification_result_error_code import (
    ProjectEnvironmentQualificationResultErrorCode,
    check_project_environment_qualification_result_error_code,
)
from ..models.project_environment_qualification_result_status import (
    ProjectEnvironmentQualificationResultStatus,
    check_project_environment_qualification_result_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectEnvironmentQualificationResult")


@_attrs_define
class ProjectEnvironmentQualificationResult:
    """Non-secret GET probe result tied to one workload deployment in the active release set."""

    workload_slug: str
    deployment_id: UUID
    status: ProjectEnvironmentQualificationResultStatus
    http_status: int | Unset = UNSET
    error_code: ProjectEnvironmentQualificationResultErrorCode | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        workload_slug = self.workload_slug

        deployment_id = str(self.deployment_id)

        status: str = self.status

        http_status = self.http_status

        error_code: str | Unset = UNSET
        if not isinstance(self.error_code, Unset):
            error_code = self.error_code

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "workload_slug": workload_slug,
                "deployment_id": deployment_id,
                "status": status,
            }
        )
        if http_status is not UNSET:
            field_dict["http_status"] = http_status
        if error_code is not UNSET:
            field_dict["error_code"] = error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workload_slug = d.pop("workload_slug")

        deployment_id = UUID(d.pop("deployment_id"))

        status = check_project_environment_qualification_result_status(d.pop("status"))

        http_status = d.pop("http_status", UNSET)

        _error_code = d.pop("error_code", UNSET)
        error_code: ProjectEnvironmentQualificationResultErrorCode | Unset
        if isinstance(_error_code, Unset):
            error_code = UNSET
        else:
            error_code = check_project_environment_qualification_result_error_code(_error_code)

        project_environment_qualification_result = cls(
            workload_slug=workload_slug,
            deployment_id=deployment_id,
            status=status,
            http_status=http_status,
            error_code=error_code,
        )

        return project_environment_qualification_result
