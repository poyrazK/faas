from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_deletion_request_version_id import (
    ObjectDeletionRequestVersionId,
    check_object_deletion_request_version_id,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectDeletionRequest")


@_attrs_define
class ObjectDeletionRequest:
    """One durable mutation; reuse the ID for retries of the same key and selector."""

    id: UUID
    key: str
    version_id: ObjectDeletionRequestVersionId | Unset = UNSET
    """Omit for ordinary deletion; null permanently removes the mutable null version."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        key = self.key

        version_id: str | Unset = UNSET
        if not isinstance(self.version_id, Unset):
            version_id = self.version_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "key": key,
            }
        )
        if version_id is not UNSET:
            field_dict["version_id"] = version_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        key = d.pop("key")

        _version_id = d.pop("version_id", UNSET)
        version_id: ObjectDeletionRequestVersionId | Unset
        if isinstance(_version_id, Unset):
            version_id = UNSET
        else:
            version_id = check_object_deletion_request_version_id(_version_id)

        object_deletion_request = cls(
            id=id,
            key=key,
            version_id=version_id,
        )

        object_deletion_request.additional_properties = d
        return object_deletion_request

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
