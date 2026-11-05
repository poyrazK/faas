from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.object_lifecycle_rule import ObjectLifecycleRule


T = TypeVar("T", bound="ObjectBucketLifecycle")


@_attrs_define
class ObjectBucketLifecycle:
    """Durable normalized lifecycle policy; removal retains revision history."""

    bucket_id: UUID
    revision: int
    rules: list[ObjectLifecycleRule]
    updated_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        bucket_id = str(self.bucket_id)

        revision = self.revision

        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "bucket_id": bucket_id,
                "revision": revision,
                "rules": rules,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_lifecycle_rule import ObjectLifecycleRule

        d = dict(src_dict)
        bucket_id = UUID(d.pop("bucket_id"))

        revision = d.pop("revision")

        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = ObjectLifecycleRule.from_dict(rules_item_data)

            rules.append(rules_item)

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        object_bucket_lifecycle = cls(
            bucket_id=bucket_id,
            revision=revision,
            rules=rules,
            updated_at=updated_at,
        )

        return object_bucket_lifecycle
