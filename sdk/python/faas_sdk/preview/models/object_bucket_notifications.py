from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_notification_rule import ObjectNotificationRule


T = TypeVar("T", bound="ObjectBucketNotifications")


@_attrs_define
class ObjectBucketNotifications:
    """Durable normalized bucket notification configuration."""

    bucket_id: UUID
    revision: int
    rules: list[ObjectNotificationRule]
    managed_rule_ids: list[str] | Unset = UNSET
    """Rules owned by declarative compute reconciliation. Full manual replacement clears ownership."""

    def to_dict(self) -> dict[str, Any]:
        bucket_id = str(self.bucket_id)

        revision = self.revision

        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        managed_rule_ids: list[str] | Unset = UNSET
        if not isinstance(self.managed_rule_ids, Unset):
            managed_rule_ids = self.managed_rule_ids

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "bucket_id": bucket_id,
                "revision": revision,
                "rules": rules,
            }
        )
        if managed_rule_ids is not UNSET:
            field_dict["managed_rule_ids"] = managed_rule_ids

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

        managed_rule_ids = cast(list[str], d.pop("managed_rule_ids", UNSET))

        object_bucket_notifications = cls(
            bucket_id=bucket_id,
            revision=revision,
            rules=rules,
            managed_rule_ids=managed_rule_ids,
        )

        return object_bucket_notifications
