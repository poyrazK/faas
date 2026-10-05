from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_response import AppResponse
    from ..models.deployment_response import DeploymentResponse
    from ..models.preview_production_changes_response import PreviewProductionChangesResponse
    from ..models.preview_resource_links_response import PreviewResourceLinksResponse


T = TypeVar("T", bound="PreviewResourceResponse")


@_attrs_define
class PreviewResourceResponse:
    """A first-class preview with production comparison and observability links."""

    app: AppResponse
    """An app: slug, type, runtime (for functions), RAM/cpu/idle-timeout config, current state, last-deploy
    pointer, per-app outbound CIDR allowlist (ADR-031 + ADR-032), and reactive scale-up trigger targets (issue #169
    / #172)."""
    changes_from_production: PreviewProductionChangesResponse
    """Non-secret preview differences from the production parent."""
    links: PreviewResourceLinksResponse
    """Public preview URL and native APIs for its logs, metrics, and effective app configuration."""
    parent: AppResponse | Unset = UNSET
    """An app: slug, type, runtime (for functions), RAM/cpu/idle-timeout config, current state, last-deploy
    pointer, per-app outbound CIDR allowlist (ADR-031 + ADR-032), and reactive scale-up trigger targets (issue #169
    / #172)."""
    latest_deployment: DeploymentResponse | Unset = UNSET
    """One deployment: id, app, source ref, build status, commit SHA, and lifecycle timestamps. The optional
    `has_overrides` and `override_*` fields are the persisted echo of the create-time overrides object (issue #460 /
    ADR-053); they round-trip via `GET /v1/apps/{slug}/deployments/{id}` so a customer can audit what their last
    deploy pinned. Env values are NEVER echoed — only the keys (`override_env_keys`); env_secrets refs ARE echoed
    because the ref shape is non-secret by design."""
    production_deployment: DeploymentResponse | Unset = UNSET
    """One deployment: id, app, source ref, build status, commit SHA, and lifecycle timestamps. The optional
    `has_overrides` and `override_*` fields are the persisted echo of the create-time overrides object (issue #460 /
    ADR-053); they round-trip via `GET /v1/apps/{slug}/deployments/{id}` so a customer can audit what their last
    deploy pinned. Env values are NEVER echoed — only the keys (`override_env_keys`); env_secrets refs ARE echoed
    because the ref shape is non-secret by design."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app.to_dict()

        changes_from_production = self.changes_from_production.to_dict()

        links = self.links.to_dict()

        parent: dict[str, Any] | Unset = UNSET
        if not isinstance(self.parent, Unset):
            parent = self.parent.to_dict()

        latest_deployment: dict[str, Any] | Unset = UNSET
        if not isinstance(self.latest_deployment, Unset):
            latest_deployment = self.latest_deployment.to_dict()

        production_deployment: dict[str, Any] | Unset = UNSET
        if not isinstance(self.production_deployment, Unset):
            production_deployment = self.production_deployment.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "changes_from_production": changes_from_production,
                "links": links,
            }
        )
        if parent is not UNSET:
            field_dict["parent"] = parent
        if latest_deployment is not UNSET:
            field_dict["latest_deployment"] = latest_deployment
        if production_deployment is not UNSET:
            field_dict["production_deployment"] = production_deployment

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_response import AppResponse
        from ..models.deployment_response import DeploymentResponse
        from ..models.preview_production_changes_response import PreviewProductionChangesResponse
        from ..models.preview_resource_links_response import PreviewResourceLinksResponse

        d = dict(src_dict)
        app = AppResponse.from_dict(d.pop("app"))

        changes_from_production = PreviewProductionChangesResponse.from_dict(d.pop("changes_from_production"))

        links = PreviewResourceLinksResponse.from_dict(d.pop("links"))

        _parent = d.pop("parent", UNSET)
        parent: AppResponse | Unset
        if isinstance(_parent, Unset):
            parent = UNSET
        else:
            parent = AppResponse.from_dict(_parent)

        _latest_deployment = d.pop("latest_deployment", UNSET)
        latest_deployment: DeploymentResponse | Unset
        if isinstance(_latest_deployment, Unset):
            latest_deployment = UNSET
        else:
            latest_deployment = DeploymentResponse.from_dict(_latest_deployment)

        _production_deployment = d.pop("production_deployment", UNSET)
        production_deployment: DeploymentResponse | Unset
        if isinstance(_production_deployment, Unset):
            production_deployment = UNSET
        else:
            production_deployment = DeploymentResponse.from_dict(_production_deployment)

        preview_resource_response = cls(
            app=app,
            changes_from_production=changes_from_production,
            links=links,
            parent=parent,
            latest_deployment=latest_deployment,
            production_deployment=production_deployment,
        )

        preview_resource_response.additional_properties = d
        return preview_resource_response

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
