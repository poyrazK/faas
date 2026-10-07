from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ObjectS3CopySource")


@_attrs_define
class ObjectS3CopySource:
    """Public copy-only authority for an owned source bucket. Placement and grant epoch are private."""

    source_bucket_id: UUID
    prefix: str
    """Literal allowed source key prefix"""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source_bucket_id = str(self.source_bucket_id)

        prefix = self.prefix

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source_bucket_id": source_bucket_id,
                "prefix": prefix,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source_bucket_id = UUID(d.pop("source_bucket_id"))

        prefix = d.pop("prefix")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        object_s3_copy_source = cls(
            source_bucket_id=source_bucket_id,
            prefix=prefix,
            created_at=created_at,
            updated_at=updated_at,
        )

        object_s3_copy_source.additional_properties = d
        return object_s3_copy_source

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
