from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ConfigSecretDifference")


@_attrs_define
class ConfigSecretDifference:
    """A secret a config rollback could not restore."""

    key: str
    target_version: int | Unset = UNSET
    current_version: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        target_version = self.target_version

        current_version = self.current_version

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
            }
        )
        if target_version is not UNSET:
            field_dict["target_version"] = target_version
        if current_version is not UNSET:
            field_dict["current_version"] = current_version

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        target_version = d.pop("target_version", UNSET)

        current_version = d.pop("current_version", UNSET)

        config_secret_difference = cls(
            key=key,
            target_version=target_version,
            current_version=current_version,
        )

        config_secret_difference.additional_properties = d
        return config_secret_difference

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
