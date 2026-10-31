from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AppRegistryCredentialResponse")


@_attrs_define
class AppRegistryCredentialResponse:
    """A sealed private-registry credential envelope: registry + username
    + timestamps. Plaintext password NEVER appears in this shape.

    """

    registry: str
    username: str
    created_at: datetime.datetime
    updated_at: datetime.datetime
    last_used_at: datetime.datetime | Unset = UNSET
    """Timestamp of the last successful authenticated pull. Omitted when the credential has not been used yet."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        registry = self.registry

        username = self.username

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        last_used_at: str | Unset = UNSET
        if not isinstance(self.last_used_at, Unset):
            last_used_at = self.last_used_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "registry": registry,
                "username": username,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if last_used_at is not UNSET:
            field_dict["last_used_at"] = last_used_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        registry = d.pop("registry")

        username = d.pop("username")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _last_used_at = d.pop("last_used_at", UNSET)
        last_used_at: datetime.datetime | Unset
        if isinstance(_last_used_at, Unset):
            last_used_at = UNSET
        else:
            last_used_at = datetime.datetime.fromisoformat(_last_used_at)

        app_registry_credential_response = cls(
            registry=registry,
            username=username,
            created_at=created_at,
            updated_at=updated_at,
            last_used_at=last_used_at,
        )

        app_registry_credential_response.additional_properties = d
        return app_registry_credential_response

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
