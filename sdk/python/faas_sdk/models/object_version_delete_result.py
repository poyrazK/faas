from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObjectVersionDeleteResult")


@_attrs_define
class ObjectVersionDeleteResult:
    """Selected immutable public version and the acknowledged marker flag after permanent deletion."""

    version_id: UUID
    delete_marker: bool
    """True when the acknowledged removal deleted a marker. A retry after removal can return false."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version_id = str(self.version_id)

        delete_marker = self.delete_marker

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version_id": version_id,
                "delete_marker": delete_marker,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version_id = UUID(d.pop("version_id"))

        delete_marker = d.pop("delete_marker")

        object_version_delete_result = cls(
            version_id=version_id,
            delete_marker=delete_marker,
        )

        object_version_delete_result.additional_properties = d
        return object_version_delete_result

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
