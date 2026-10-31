from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_promotion_workload_response_status import (
    ProjectEnvironmentPromotionWorkloadResponseStatus,
    check_project_environment_promotion_workload_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectEnvironmentPromotionWorkloadResponse")


@_attrs_define
class ProjectEnvironmentPromotionWorkloadResponse:
    """Result for one workload in an environment promotion."""

    workload_slug: str
    workload_name: str
    status: ProjectEnvironmentPromotionWorkloadResponseStatus
    source_deployment_id: str | Unset = UNSET
    target_deployment_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workload_slug = self.workload_slug

        workload_name = self.workload_name

        status: str = self.status

        source_deployment_id = self.source_deployment_id

        target_deployment_id = self.target_deployment_id

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
        if target_deployment_id is not UNSET:
            field_dict["target_deployment_id"] = target_deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        workload_slug = d.pop("workload_slug")

        workload_name = d.pop("workload_name")

        status = check_project_environment_promotion_workload_response_status(d.pop("status"))

        source_deployment_id = d.pop("source_deployment_id", UNSET)

        target_deployment_id = d.pop("target_deployment_id", UNSET)

        project_environment_promotion_workload_response = cls(
            workload_slug=workload_slug,
            workload_name=workload_name,
            status=status,
            source_deployment_id=source_deployment_id,
            target_deployment_id=target_deployment_id,
        )

        project_environment_promotion_workload_response.additional_properties = d
        return project_environment_promotion_workload_response

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
