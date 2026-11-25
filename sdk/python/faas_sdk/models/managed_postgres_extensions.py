from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_postgres_extension_version import ManagedPostgresExtensionVersion


T = TypeVar("T", bound="ManagedPostgresExtensions")


@_attrs_define
class ManagedPostgresExtensions:
    installed: list[ManagedPostgresExtensionVersion]
    available: list[ManagedPostgresExtensionVersion]
    incompatible_extensions: list[ManagedPostgresExtensionVersion] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        installed = []
        for installed_item_data in self.installed:
            installed_item = installed_item_data.to_dict()
            installed.append(installed_item)

        available = []
        for available_item_data in self.available:
            available_item = available_item_data.to_dict()
            available.append(available_item)

        incompatible_extensions: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.incompatible_extensions, Unset):
            incompatible_extensions = []
            for incompatible_extensions_item_data in self.incompatible_extensions:
                incompatible_extensions_item = incompatible_extensions_item_data.to_dict()
                incompatible_extensions.append(incompatible_extensions_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "installed": installed,
                "available": available,
            }
        )
        if incompatible_extensions is not UNSET:
            field_dict["incompatible_extensions"] = incompatible_extensions

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_extension_version import ManagedPostgresExtensionVersion

        d = dict(src_dict)
        installed = []
        _installed = d.pop("installed")
        for installed_item_data in _installed:
            installed_item = ManagedPostgresExtensionVersion.from_dict(installed_item_data)

            installed.append(installed_item)

        available = []
        _available = d.pop("available")
        for available_item_data in _available:
            available_item = ManagedPostgresExtensionVersion.from_dict(available_item_data)

            available.append(available_item)

        _incompatible_extensions = d.pop("incompatible_extensions", UNSET)
        incompatible_extensions: list[ManagedPostgresExtensionVersion] | Unset = UNSET
        if _incompatible_extensions is not UNSET:
            incompatible_extensions = []
            for incompatible_extensions_item_data in _incompatible_extensions:
                incompatible_extensions_item = ManagedPostgresExtensionVersion.from_dict(
                    incompatible_extensions_item_data
                )

                incompatible_extensions.append(incompatible_extensions_item)

        managed_postgres_extensions = cls(
            installed=installed,
            available=available,
            incompatible_extensions=incompatible_extensions,
        )

        managed_postgres_extensions.additional_properties = d
        return managed_postgres_extensions

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
