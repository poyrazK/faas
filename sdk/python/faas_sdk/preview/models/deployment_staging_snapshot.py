from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.deployment_staging_snapshot_settings import DeploymentStagingSnapshotSettings


T = TypeVar("T", bound="DeploymentStagingSnapshot")


@_attrs_define
class DeploymentStagingSnapshot:
    """An atomic snapshot for journaling selected deployment policies before staging."""

    app_id: UUID
    revision: int
    latest_deployment_id: str
    """The latest deployment ID, or an empty string when no deployment exists."""
    settings: DeploymentStagingSnapshotSettings
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        revision = self.revision

        latest_deployment_id = self.latest_deployment_id

        settings = self.settings.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "revision": revision,
                "latest_deployment_id": latest_deployment_id,
                "settings": settings,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.deployment_staging_snapshot_settings import DeploymentStagingSnapshotSettings

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        revision = d.pop("revision")

        latest_deployment_id = d.pop("latest_deployment_id")

        settings = DeploymentStagingSnapshotSettings.from_dict(d.pop("settings"))

        deployment_staging_snapshot = cls(
            app_id=app_id,
            revision=revision,
            latest_deployment_id=latest_deployment_id,
            settings=settings,
        )

        deployment_staging_snapshot.additional_properties = d
        return deployment_staging_snapshot

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
