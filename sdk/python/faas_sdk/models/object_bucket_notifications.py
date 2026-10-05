from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.object_notification_rule import ObjectNotificationRule


T = TypeVar("T", bound="ObjectBucketNotifications")


@_attrs_define
class ObjectBucketNotifications:
    """Durable normalized bucket notification configuration."""

    bucket_id: UUID
    revision: int
    rules: list[ObjectNotificationRule]

    def to_dict(self) -> dict[str, Any]:
        bucket_id = str(self.bucket_id)

        revision = self.revision

        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "bucket_id": bucket_id,
                "revision": revision,
                "rules": rules,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_notification_rule import ObjectNotificationRule

        d = dict(src_dict)
        bucket_id = UUID(d.pop("bucket_id"))

        revision = d.pop("revision")

        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = ObjectNotificationRule.from_dict(rules_item_data)

            rules.append(rules_item)

        object_bucket_notifications = cls(
            bucket_id=bucket_id,
            revision=revision,
            rules=rules,
        )

        return object_bucket_notifications
