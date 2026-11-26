from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ProjectRollbackProtection")


@_attrs_define
class ProjectRollbackProtection:
    """Saved retention window and storage size for a protected deploy group."""

    deploy_group_id: UUID
    expires_at: datetime.datetime
    layer_bytes: int
    """Bytes of unique root filesystem and sidecar layers held for this deploy."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deploy_group_id = str(self.deploy_group_id)

        expires_at = self.expires_at.isoformat()

        layer_bytes = self.layer_bytes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deploy_group_id": deploy_group_id,
                "expires_at": expires_at,
                "layer_bytes": layer_bytes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deploy_group_id = UUID(d.pop("deploy_group_id"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        layer_bytes = d.pop("layer_bytes")

        project_rollback_protection = cls(
            deploy_group_id=deploy_group_id,
            expires_at=expires_at,
            layer_bytes=layer_bytes,
        )

        project_rollback_protection.additional_properties = d
        return project_rollback_protection

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
