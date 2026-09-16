from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectWorkloadResponse")


@_attrs_define
class ProjectWorkloadResponse:
    """Live app attached to a durable project and its latest release state."""

    slug: str
    workload_name: str
    status: str
    deployment_status: str | Unset = UNSET
    rollout_state: str | Unset = UNSET
    build_status: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        workload_name = self.workload_name

        status = self.status

        deployment_status = self.deployment_status

        rollout_state = self.rollout_state

        build_status = self.build_status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "workload_name": workload_name,
                "status": status,
            }
        )
        if deployment_status is not UNSET:
            field_dict["deployment_status"] = deployment_status
        if rollout_state is not UNSET:
            field_dict["rollout_state"] = rollout_state
        if build_status is not UNSET:
            field_dict["build_status"] = build_status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        slug = d.pop("slug")

        workload_name = d.pop("workload_name")

        status = d.pop("status")

        deployment_status = d.pop("deployment_status", UNSET)

        rollout_state = d.pop("rollout_state", UNSET)

        build_status = d.pop("build_status", UNSET)

        project_workload_response = cls(
            slug=slug,
            workload_name=workload_name,
            status=status,
            deployment_status=deployment_status,
            rollout_state=rollout_state,
            build_status=build_status,
        )

        project_workload_response.additional_properties = d
        return project_workload_response

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
