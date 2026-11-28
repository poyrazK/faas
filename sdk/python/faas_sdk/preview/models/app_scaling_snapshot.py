from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_scaling_settings import AppScalingSettings


T = TypeVar("T", bound="AppScalingSnapshot")


@_attrs_define
class AppScalingSnapshot:
    """Current desired scaling configuration and its compare-and-swap revision."""

    app_id: UUID
    scope: str
    revision: int
    settings: AppScalingSettings
    """Stored RAM, CPU, concurrency and instance-floor settings."""
    environment_id: UUID | Unset = UNSET
    """Immutable identity for a named environment."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        scope = self.scope

        revision = self.revision

        settings = self.settings.to_dict()

        environment_id: str | Unset = UNSET
        if not isinstance(self.environment_id, Unset):
            environment_id = str(self.environment_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "scope": scope,
                "revision": revision,
                "settings": settings,
            }
        )
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_scaling_settings import AppScalingSettings

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        revision = d.pop("revision")

        settings = AppScalingSettings.from_dict(d.pop("settings"))

        _environment_id = d.pop("environment_id", UNSET)
        environment_id: UUID | Unset
        if isinstance(_environment_id, Unset):
            environment_id = UNSET
        else:
            environment_id = UUID(_environment_id)

        app_scaling_snapshot = cls(
            app_id=app_id,
            scope=scope,
            revision=revision,
            settings=settings,
            environment_id=environment_id,
        )

        app_scaling_snapshot.additional_properties = d
        return app_scaling_snapshot

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
