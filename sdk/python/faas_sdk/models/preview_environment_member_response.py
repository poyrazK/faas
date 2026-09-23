from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="PreviewEnvironmentMemberResponse")


@_attrs_define
class PreviewEnvironmentMemberResponse:
    """One expected preview workload and its latest deployment for the recorded commit."""

    app_id: UUID
    slug: str
    """Empty if the recorded app is missing."""
    workload_name: str
    app_status: str
    """Includes missing when the recorded app is unavailable."""
    preview_state: str
    deployment_id: str
    """Empty until a deployment for the recorded commit exists."""
    deployment_status: str
    """Missing until a deployment for the recorded commit exists."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        slug = self.slug

        workload_name = self.workload_name

        app_status = self.app_status

        preview_state = self.preview_state

        deployment_id = self.deployment_id

        deployment_status = self.deployment_status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "slug": slug,
                "workload_name": workload_name,
                "app_status": app_status,
                "preview_state": preview_state,
                "deployment_id": deployment_id,
                "deployment_status": deployment_status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        slug = d.pop("slug")

        workload_name = d.pop("workload_name")

        app_status = d.pop("app_status")

        preview_state = d.pop("preview_state")

        deployment_id = d.pop("deployment_id")

        deployment_status = d.pop("deployment_status")

        preview_environment_member_response = cls(
            app_id=app_id,
            slug=slug,
            workload_name=workload_name,
            app_status=app_status,
            preview_state=preview_state,
            deployment_id=deployment_id,
            deployment_status=deployment_status,
        )

        preview_environment_member_response.additional_properties = d
        return preview_environment_member_response

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
