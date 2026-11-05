from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_version import ObjectVersion


T = TypeVar("T", bound="ObjectVersionList")


@_attrs_define
class ObjectVersionList:
    """An owned public version page with paired continuation markers."""

    items: list[ObjectVersion]
    common_prefixes: list[str]
    next_key_marker: str | Unset = UNSET
    next_version_id_marker: str | Unset = UNSET
    """Owned public version selector to resume with next_key_marker."""

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        common_prefixes = self.common_prefixes

        next_key_marker = self.next_key_marker

        next_version_id_marker = self.next_version_id_marker

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "items": items,
                "common_prefixes": common_prefixes,
            }
        )
        if next_key_marker is not UNSET:
            field_dict["next_key_marker"] = next_key_marker
        if next_version_id_marker is not UNSET:
            field_dict["next_version_id_marker"] = next_version_id_marker

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_version import ObjectVersion

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ObjectVersion.from_dict(items_item_data)

            items.append(items_item)

        common_prefixes = cast(list[str], d.pop("common_prefixes"))

        next_key_marker = d.pop("next_key_marker", UNSET)

        next_version_id_marker = d.pop("next_version_id_marker", UNSET)

        object_version_list = cls(
            items=items,
            common_prefixes=common_prefixes,
            next_key_marker=next_key_marker,
            next_version_id_marker=next_version_id_marker,
        )

        return object_version_list
