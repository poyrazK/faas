from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.object_lifecycle_rule import ObjectLifecycleRule


T = TypeVar("T", bound="ObjectBucketLifecycleRequest")


@_attrs_define
class ObjectBucketLifecycleRequest:
    """Complete lifecycle rule replacement; use DELETE to clear the configuration."""

    rules: list[ObjectLifecycleRule]

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
        from ..models.object_lifecycle_rule import ObjectLifecycleRule

        d = dict(src_dict)
        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = ObjectLifecycleRule.from_dict(rules_item_data)

            rules.append(rules_item)

        object_bucket_lifecycle_request = cls(
            rules=rules,
        )

        return object_bucket_lifecycle_request
