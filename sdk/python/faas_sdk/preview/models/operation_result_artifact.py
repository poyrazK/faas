from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationResultArtifact")


@_attrs_define
class OperationResultArtifact:
    """Verified private result reference, from a managed object or direct Job upload."""

    id: UUID
    name: str
    uri: str
    """Managed obj:// source or opaque operation://<operation UUID>/artifacts/<artifact UUID> direct-upload
    reference; never a signed URL or physical storage key."""
    size_bytes: int
    sha256: str
    expires_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        uri = self.uri

        size_bytes = self.size_bytes

        sha256 = self.sha256

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "uri": uri,
                "size_bytes": size_bytes,
                "sha256": sha256,
            }
        )
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        uri = d.pop("uri")

        size_bytes = d.pop("size_bytes")

        sha256 = d.pop("sha256")

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        operation_result_artifact = cls(
            id=id,
            name=name,
            uri=uri,
            size_bytes=size_bytes,
            sha256=sha256,
            expires_at=expires_at,
        )

        operation_result_artifact.additional_properties = d
        return operation_result_artifact

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
