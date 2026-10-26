from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.object_bucket_cors_rule import ObjectBucketCORSRule


T = TypeVar("T", bound="ObjectBucketCORSRequest")


@_attrs_define
class ObjectBucketCORSRequest:
    """The complete set of CORS rules for a bucket."""

    rules: list[ObjectBucketCORSRule]

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
        from ..models.object_bucket_cors_rule import ObjectBucketCORSRule

        d = dict(src_dict)
        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = ObjectBucketCORSRule.from_dict(rules_item_data)

            rules.append(rules_item)

        object_bucket_cors_request = cls(
            rules=rules,
        )

        return object_bucket_cors_request
