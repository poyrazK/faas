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
    public: bool | Unset = False
    """Serve objects anonymously on the app hostname."""
    serve_at: str | Unset = UNSET
    """Immutable app path mounted when public is true."""
    object_lock: bool | Unset = False
    """Create the bucket with Object Lock. Permanent; the bucket keeps every object version. Some storage accepts
    Object Lock only when the bucket is created, so choose it here."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        scope = self.scope

        region = self.region

        public = self.public

        serve_at = self.serve_at

        object_lock = self.object_lock

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
        if public is not UNSET:
            field_dict["public"] = public
        if serve_at is not UNSET:
            field_dict["serve_at"] = serve_at
        if object_lock is not UNSET:
            field_dict["object_lock"] = object_lock

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        scope = d.pop("scope", UNSET)

        region = d.pop("region", UNSET)

        public = d.pop("public", UNSET)

        serve_at = d.pop("serve_at", UNSET)

        object_lock = d.pop("object_lock", UNSET)

        create_object_bucket_body = cls(
            name=name,
            scope=scope,
            region=region,
            public=public,
            serve_at=serve_at,
            object_lock=object_lock,
        )

        return create_object_bucket_body
