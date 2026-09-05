from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateObjectBucketBody")


@_attrs_define
class CreateObjectBucketBody:
    name: str
    scope: str | Unset = "default"
    region: str | Unset = UNSET
    """Gregale region, not upstream signing region. Omit to use the configured default."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        scope = self.scope

        region = self.region

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
            }
        )
        if scope is not UNSET:
            field_dict["scope"] = scope
        if region is not UNSET:
            field_dict["region"] = region

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        scope = d.pop("scope", UNSET)

        region = d.pop("region", UNSET)

        create_object_bucket_body = cls(
            name=name,
            scope=scope,
            region=region,
        )

        return create_object_bucket_body
