from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_lifecycle_filter_tags import ObjectLifecycleFilterTags


T = TypeVar("T", bound="ObjectLifecycleFilter")


@_attrs_define
class ObjectLifecycleFilter:
    """Empty filter selects all keys. Prefix and tags are conjunctive; at most ten tags are supported."""

    prefix: str | Unset = UNSET
    """Exact key prefix; UTF-8 bytes are bounded by the portable key limit."""
    tags: ObjectLifecycleFilterTags | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        prefix = self.prefix

        tags: dict[str, Any] | Unset = UNSET
        if not isinstance(self.tags, Unset):
            tags = self.tags.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if prefix is not UNSET:
            field_dict["prefix"] = prefix
        if tags is not UNSET:
            field_dict["tags"] = tags

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_lifecycle_filter_tags import ObjectLifecycleFilterTags

        d = dict(src_dict)
        prefix = d.pop("prefix", UNSET)

        _tags = d.pop("tags", UNSET)
        tags: ObjectLifecycleFilterTags | Unset
        if isinstance(_tags, Unset):
            tags = UNSET
        else:
            tags = ObjectLifecycleFilterTags.from_dict(_tags)

        object_lifecycle_filter = cls(
            prefix=prefix,
            tags=tags,
        )

        return object_lifecycle_filter
