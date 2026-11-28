from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.object_notification_rule import ObjectNotificationRule


T = TypeVar("T", bound="ObjectBucketNotificationsRequest")


@_attrs_define
class ObjectBucketNotificationsRequest:
    """Complete atomic replacement; an empty array clears notification intent."""

    rules: list[ObjectNotificationRule]

    def to_dict(self) -> dict[str, Any]:
        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "rules": rules,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_notification_rule import ObjectNotificationRule

        d = dict(src_dict)
        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = ObjectNotificationRule.from_dict(rules_item_data)

            rules.append(rules_item)

        object_bucket_notifications_request = cls(
            rules=rules,
        )

        return object_bucket_notifications_request
