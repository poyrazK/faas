from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="PutAppRegistryCredentialRequest")


@_attrs_define
class PutAppRegistryCredentialRequest:
    """Set a private-registry Basic Auth credential: normalized registry
    host + username + plaintext password. The password is sealed
    server-side under namespace `"registry_creds"` against the host
    X25519 recipient and never persisted in plaintext.

    """

    registry: str
    """Registry host. Must include explicit `https://` prefix (schemeless + http:// are rejected per ADR-062
    §https-only clarification; customer's Basic Auth never leaves the box over cleartext). Trailing slash optional;
    embedded path / query / fragment rejected."""
    username: str
    """Basic Auth username (metadata, NOT sealed)."""
    password: str
    """Plaintext Basic Auth password. Sealed server-side; never persisted or returned."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        registry = self.registry

        username = self.username

        password = self.password

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "registry": registry,
                "username": username,
                "password": password,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        registry = d.pop("registry")

        username = d.pop("username")

        password = d.pop("password")

        put_app_registry_credential_request = cls(
            registry=registry,
            username=username,
            password=password,
        )

        put_app_registry_credential_request.additional_properties = d
        return put_app_registry_credential_request

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
