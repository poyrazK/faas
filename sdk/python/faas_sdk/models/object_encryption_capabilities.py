from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_encryption_capabilities_algorithms_item import (
    ObjectEncryptionCapabilitiesAlgorithmsItem,
    check_object_encryption_capabilities_algorithms_item,
)

T = TypeVar("T", bound="ObjectEncryptionCapabilities")


@_attrs_define
class ObjectEncryptionCapabilities:
    """Operator enrollment for this bucket placement. Contains no native key identity, key material or permission
    assertion.

    """

    algorithms: list[ObjectEncryptionCapabilitiesAlgorithmsItem]
    key_ids: list[str]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        algorithms = []
        for algorithms_item_data in self.algorithms:
            algorithms_item: str = algorithms_item_data
            algorithms.append(algorithms_item)

        key_ids = self.key_ids

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "algorithms": algorithms,
                "key_ids": key_ids,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        algorithms = []
        _algorithms = d.pop("algorithms")
        for algorithms_item_data in _algorithms:
            algorithms_item = check_object_encryption_capabilities_algorithms_item(algorithms_item_data)

            algorithms.append(algorithms_item)

        key_ids = cast(list[str], d.pop("key_ids"))

        object_encryption_capabilities = cls(
            algorithms=algorithms,
            key_ids=key_ids,
        )

        object_encryption_capabilities.additional_properties = d
        return object_encryption_capabilities

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
