from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AppResponseLabels")


@_attrs_define
class AppResponseLabels:
    """Key/value labels with Kubernetes syntax, at most 32. A key is an optional DNS-subdomain prefix and a slash, then a
    name of 1-63 letters, digits, '.', '_' or '-' that starts and ends with a letter or digit; a value is empty or 1-63
    of the same. Keys under gregale.dev/ belong to Gregale. Labels are not secret: they appear in lists and audit
    events. Present while the resource-labels capability is on for the caller.

        Example:
            {'team': 'billing', 'tier': 'web'}

    """

    additional_properties: dict[str, str] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_response_labels = cls()

        app_response_labels.additional_properties = d
        return app_response_labels

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> str:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: str) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
