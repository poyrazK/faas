from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.preview_production_changes_response import PreviewProductionChangesResponse
    from ..models.preview_resource_links_response import PreviewResourceLinksResponse


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
    expires_at: datetime.datetime | Unset = UNSET
    """Preview workload expiration."""
    changes_from_production: PreviewProductionChangesResponse | Unset = UNSET
    """Non-secret preview differences from the production parent."""
    links: PreviewResourceLinksResponse | Unset = UNSET
    """Public preview URL and native APIs for its logs, metrics, and effective app configuration."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        slug = self.slug

        workload_name = self.workload_name

        app_status = self.app_status

        preview_state = self.preview_state

        deployment_id = self.deployment_id

        deployment_status = self.deployment_status

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        changes_from_production: dict[str, Any] | Unset = UNSET
        if not isinstance(self.changes_from_production, Unset):
            changes_from_production = self.changes_from_production.to_dict()

        links: dict[str, Any] | Unset = UNSET
        if not isinstance(self.links, Unset):
            links = self.links.to_dict()

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
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if changes_from_production is not UNSET:
            field_dict["changes_from_production"] = changes_from_production
        if links is not UNSET:
            field_dict["links"] = links

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.preview_production_changes_response import PreviewProductionChangesResponse
        from ..models.preview_resource_links_response import PreviewResourceLinksResponse

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        slug = d.pop("slug")

        workload_name = d.pop("workload_name")

        app_status = d.pop("app_status")

        preview_state = d.pop("preview_state")

        deployment_id = d.pop("deployment_id")

        deployment_status = d.pop("deployment_status")

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        _changes_from_production = d.pop("changes_from_production", UNSET)
        changes_from_production: PreviewProductionChangesResponse | Unset
        if isinstance(_changes_from_production, Unset):
            changes_from_production = UNSET
        else:
            changes_from_production = PreviewProductionChangesResponse.from_dict(_changes_from_production)

        _links = d.pop("links", UNSET)
        links: PreviewResourceLinksResponse | Unset
        if isinstance(_links, Unset):
            links = UNSET
        else:
            links = PreviewResourceLinksResponse.from_dict(_links)

        preview_environment_member_response = cls(
            app_id=app_id,
            slug=slug,
            workload_name=workload_name,
            app_status=app_status,
            preview_state=preview_state,
            deployment_id=deployment_id,
            deployment_status=deployment_status,
            expires_at=expires_at,
            changes_from_production=changes_from_production,
            links=links,
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
