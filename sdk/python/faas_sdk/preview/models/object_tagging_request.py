from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.object_tagging_request_tags import ObjectTaggingRequestTags


T = TypeVar("T", bound="ObjectTaggingRequest")


@_attrs_define
class ObjectTaggingRequest:
    """Complete replacement object tag set, including an empty set to clear all tags."""

    tags: ObjectTaggingRequestTags
    """Complete replacement set. Empty removes all tags. Keys and values use the portable UTF-8 byte limits."""

    def to_dict(self) -> dict[str, Any]:
        tags = self.tags.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "tags": tags,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_tagging_request_tags import ObjectTaggingRequestTags

        d = dict(src_dict)
        tags = ObjectTaggingRequestTags.from_dict(d.pop("tags"))

        object_tagging_request = cls(
            tags=tags,
        )

        return object_tagging_request
