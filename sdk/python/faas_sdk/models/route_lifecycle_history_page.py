from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_lifecycle_history_entry import RouteLifecycleHistoryEntry


T = TypeVar("T", bound="RouteLifecycleHistoryPage")


@_attrs_define
class RouteLifecycleHistoryPage:
    app_id: str
    entries: list[RouteLifecycleHistoryEntry]
    next_cursor: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "entries": entries,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_lifecycle_history_entry import RouteLifecycleHistoryEntry

        d = dict(src_dict)
        app_id = d.pop("app_id")

        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = RouteLifecycleHistoryEntry.from_dict(entries_item_data)

            entries.append(entries_item)

        next_cursor = d.pop("next_cursor", UNSET)

        route_lifecycle_history_page = cls(
            app_id=app_id,
            entries=entries,
            next_cursor=next_cursor,
        )

        route_lifecycle_history_page.additional_properties = d
        return route_lifecycle_history_page

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
