from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.object_bucket_cors_rule_allowed_methods_item import (
    ObjectBucketCORSRuleAllowedMethodsItem,
    check_object_bucket_cors_rule_allowed_methods_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectBucketCORSRule")


@_attrs_define
class ObjectBucketCORSRule:
    """One S3 CORSRule. Origins are "*" or an http(s) origin with at most one "*"; header patterns allow one "*"."""

    allowed_origins: list[str]
    allowed_methods: list[ObjectBucketCORSRuleAllowedMethodsItem]
    id: str | Unset = UNSET
    allowed_headers: list[str] | Unset = UNSET
    expose_headers: list[str] | Unset = UNSET
    max_age_seconds: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        allowed_origins = self.allowed_origins

        allowed_methods = []
        for allowed_methods_item_data in self.allowed_methods:
            allowed_methods_item: str = allowed_methods_item_data
            allowed_methods.append(allowed_methods_item)

        id = self.id

        allowed_headers: list[str] | Unset = UNSET
        if not isinstance(self.allowed_headers, Unset):
            allowed_headers = self.allowed_headers

        expose_headers: list[str] | Unset = UNSET
        if not isinstance(self.expose_headers, Unset):
            expose_headers = self.expose_headers

        max_age_seconds = self.max_age_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "allowed_origins": allowed_origins,
                "allowed_methods": allowed_methods,
            }
        )
        if id is not UNSET:
            field_dict["id"] = id
        if allowed_headers is not UNSET:
            field_dict["allowed_headers"] = allowed_headers
        if expose_headers is not UNSET:
            field_dict["expose_headers"] = expose_headers
        if max_age_seconds is not UNSET:
            field_dict["max_age_seconds"] = max_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        allowed_origins = cast(list[str], d.pop("allowed_origins"))

        allowed_methods = []
        _allowed_methods = d.pop("allowed_methods")
        for allowed_methods_item_data in _allowed_methods:
            allowed_methods_item = check_object_bucket_cors_rule_allowed_methods_item(allowed_methods_item_data)

            allowed_methods.append(allowed_methods_item)

        id = d.pop("id", UNSET)

        allowed_headers = cast(list[str], d.pop("allowed_headers", UNSET))

        expose_headers = cast(list[str], d.pop("expose_headers", UNSET))

        max_age_seconds = d.pop("max_age_seconds", UNSET)

        object_bucket_cors_rule = cls(
            allowed_origins=allowed_origins,
            allowed_methods=allowed_methods,
            id=id,
            allowed_headers=allowed_headers,
            expose_headers=expose_headers,
            max_age_seconds=max_age_seconds,
        )

        return object_bucket_cors_rule
