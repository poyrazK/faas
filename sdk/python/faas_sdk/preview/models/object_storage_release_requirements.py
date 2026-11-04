from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_storage_release_requirements_additional_property import (
    ObjectStorageReleaseRequirementsAdditionalProperty,
    check_object_storage_release_requirements_additional_property,
)

T = TypeVar("T", bound="ObjectStorageReleaseRequirements")


@_attrs_define
class ObjectStorageReleaseRequirements:
    """Managed environment prefix to required canary mode in the selected scope. Prefixes match ^[A-Z][A-Z0-9_]{0,47}$.
    read covers listing; read_write requires byte transfer; write and read_write require successful cleanup. No implicit
    probes or writes.

    """

    additional_properties: dict[str, ObjectStorageReleaseRequirementsAdditionalProperty] = _attrs_field(
        init=False, factory=dict
    )

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        for prop_name, prop in self.additional_properties.items():
            field_dict[prop_name] = prop

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        object_storage_release_requirements = cls()

        additional_properties = {}
        for prop_name, prop_dict in d.items():
            additional_property = check_object_storage_release_requirements_additional_property(prop_dict)

            additional_properties[prop_name] = additional_property

        object_storage_release_requirements.additional_properties = additional_properties
        return object_storage_release_requirements

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> ObjectStorageReleaseRequirementsAdditionalProperty:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: ObjectStorageReleaseRequirementsAdditionalProperty) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
