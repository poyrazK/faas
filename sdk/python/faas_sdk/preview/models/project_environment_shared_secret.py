from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ProjectEnvironmentSharedSecret")


@_attrs_define
class ProjectEnvironmentSharedSecret:
    """Metadata of one secret shared by every service of a project environment. The value is write-only."""

    key: str
    version: int
    """Increases on every value change."""
    kid: str
    """Recipient of the fleet key that sealed the value."""
    value_hash: str
    """HMAC fingerprint of the value, for comparing values without revealing them."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        version = self.version

        kid = self.kid

        value_hash = self.value_hash

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "version": version,
                "kid": kid,
                "value_hash": value_hash,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        version = d.pop("version")

        kid = d.pop("kid")

        value_hash = d.pop("value_hash")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        project_environment_shared_secret = cls(
            key=key,
            version=version,
            kid=kid,
            value_hash=value_hash,
            created_at=created_at,
            updated_at=updated_at,
        )

        project_environment_shared_secret.additional_properties = d
        return project_environment_shared_secret

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
