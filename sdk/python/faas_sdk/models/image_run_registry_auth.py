from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ImageRunRegistryAuth")


@_attrs_define
class ImageRunRegistryAuth:
    """ADR-1790: credential for a private image's registry. apid stores it sealed on the image's runner job for that
    registry host, exactly as PUT /v1/jobs/{name}/registry-credentials does, and never returns it. Every run that
    supplies it refreshes it.

    """

    username: str
    """Registry account or robot name for the image's registry."""
    password: str
    """Registry password or access token; prefer a short-lived read-only token."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        username = self.username

        password = self.password

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "username": username,
                "password": password,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        username = d.pop("username")

        password = d.pop("password")

        image_run_registry_auth = cls(
            username=username,
            password=password,
        )

        image_run_registry_auth.additional_properties = d
        return image_run_registry_auth

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
