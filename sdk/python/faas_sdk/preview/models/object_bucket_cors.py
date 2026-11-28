from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.object_bucket_cors_rule import ObjectBucketCORSRule


T = TypeVar("T", bound="ObjectBucketCORS")


@_attrs_define
class ObjectBucketCORS:
    """A bucket's CORS rules; revision 0 means none are set."""

    bucket_id: UUID
    rules: list[ObjectBucketCORSRule]
    revision: int
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        bucket_id = str(self.bucket_id)

        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        revision = self.revision

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "bucket_id": bucket_id,
                "rules": rules,
                "revision": revision,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_bucket_cors_rule import ObjectBucketCORSRule

        d = dict(src_dict)
        bucket_id = UUID(d.pop("bucket_id"))

        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = ObjectBucketCORSRule.from_dict(rules_item_data)

            rules.append(rules_item)

        revision = d.pop("revision")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        object_bucket_cors = cls(
            bucket_id=bucket_id,
            rules=rules,
            revision=revision,
            updated_at=updated_at,
        )

        object_bucket_cors.additional_properties = d
        return object_bucket_cors

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
