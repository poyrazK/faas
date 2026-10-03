from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="CreateGlobalAppRequest")


@_attrs_define
class CreateGlobalAppRequest:
    """Global identity registration with no regional provisioning."""

    slug: str

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "slug": slug,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        slug = d.pop("slug")

        create_global_app_request = cls(
            slug=slug,
        )

        return create_global_app_request
