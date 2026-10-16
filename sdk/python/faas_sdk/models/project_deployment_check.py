from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_deployment_check_status import ProjectDeploymentCheckStatus, check_project_deployment_check_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectDeploymentCheck")


@_attrs_define
class ProjectDeploymentCheck:
    """Public deployment readiness finding with its status, observed limits and suggested remedy."""

    code: str
    status: ProjectDeploymentCheckStatus
    message: str
    workload: str | Unset = UNSET
    remedy: str | Unset = UNSET
    observed: int | Unset = UNSET
    limit: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        status: str = self.status

        message = self.message

        workload = self.workload

        remedy = self.remedy

        observed = self.observed

        limit = self.limit

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "code": code,
                "status": status,
                "message": message,
            }
        )
        if workload is not UNSET:
            field_dict["workload"] = workload
        if remedy is not UNSET:
            field_dict["remedy"] = remedy
        if observed is not UNSET:
            field_dict["observed"] = observed
        if limit is not UNSET:
            field_dict["limit"] = limit

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        status = check_project_deployment_check_status(d.pop("status"))

        message = d.pop("message")

        workload = d.pop("workload", UNSET)

        remedy = d.pop("remedy", UNSET)

        observed = d.pop("observed", UNSET)

        limit = d.pop("limit", UNSET)

        project_deployment_check = cls(
            code=code,
            status=status,
            message=message,
            workload=workload,
            remedy=remedy,
            observed=observed,
            limit=limit,
        )

        project_deployment_check.additional_properties = d
        return project_deployment_check

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
