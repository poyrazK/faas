from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="SecretFile")


@_attrs_define
class SecretFile:
    """An app secret guest-init writes to /run/secrets/<name> in the main workload (ADR-20261023090000), from a Compose
    service's secrets. Names the secret; its value is never returned.

    """

    secret: str
    """App secret whose value the file holds."""
    name: str
    """File name in /run/secrets."""
    mode: int | Unset = UNSET
    """Permission bits; omitted means 0444."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        secret = self.secret

        name = self.name

        mode = self.mode

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "secret": secret,
                "name": name,
            }
        )
        if mode is not UNSET:
            field_dict["mode"] = mode

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        secret = d.pop("secret")

        name = d.pop("name")

        mode = d.pop("mode", UNSET)

        secret_file = cls(
            secret=secret,
            name=name,
            mode=mode,
        )

        secret_file.additional_properties = d
        return secret_file

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
