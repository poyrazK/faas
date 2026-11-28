from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profile_deployment_policy_config import ProfileDeploymentPolicyConfig


T = TypeVar("T", bound="ProfileDeploymentPolicy")


@_attrs_define
class ProfileDeploymentPolicy:
    """App-owned CPU-check configuration. Revision zero represents an unsaved disabled policy; updated_at appears after
    saving.

    """

    app_id: UUID
    revision: int
    config: ProfileDeploymentPolicyConfig
    """Opt-in background comparison policy. Collection must already be enabled on applications. Runtime filters
    apply to both deployments; capture coverage is not instrumentation completeness."""
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        revision = self.revision

        config = self.config.to_dict()

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "revision": revision,
                "config": config,
            }
        )
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_deployment_policy_config import ProfileDeploymentPolicyConfig

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        revision = d.pop("revision")

        config = ProfileDeploymentPolicyConfig.from_dict(d.pop("config"))

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        profile_deployment_policy = cls(
            app_id=app_id,
            revision=revision,
            config=config,
            updated_at=updated_at,
        )

        profile_deployment_policy.additional_properties = d
        return profile_deployment_policy

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
