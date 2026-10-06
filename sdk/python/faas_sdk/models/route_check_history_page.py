from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_check_history_summary import RouteCheckHistorySummary


T = TypeVar("T", bound="RouteCheckHistoryPage")


@_attrs_define
class RouteCheckHistoryPage:
    """Deployment-scoped page of retained route check summaries and an optional continuation cursor."""

    app_id: UUID
    deployment_id: UUID
    entries: list[RouteCheckHistorySummary]
    next_cursor: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        next_cursor: str | Unset = UNSET
        if not isinstance(self.next_cursor, Unset):
            next_cursor = str(self.next_cursor)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "deployment_id": deployment_id,
                "entries": entries,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_check_history_summary import RouteCheckHistorySummary

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = RouteCheckHistorySummary.from_dict(entries_item_data)

            entries.append(entries_item)

        _next_cursor = d.pop("next_cursor", UNSET)
        next_cursor: UUID | Unset
        if isinstance(_next_cursor, Unset):
            next_cursor = UNSET
        else:
            next_cursor = UUID(_next_cursor)

        route_check_history_page = cls(
            app_id=app_id,
            deployment_id=deployment_id,
            entries=entries,
            next_cursor=next_cursor,
        )

        route_check_history_page.additional_properties = d
        return route_check_history_page

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
