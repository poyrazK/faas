from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.app_health_history_page_scope import AppHealthHistoryPageScope, check_app_health_history_page_scope
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_health_history_entry import AppHealthHistoryEntry
    from ..models.app_health_response import AppHealthResponse


T = TypeVar("T", bound="AppHealthHistoryPage")


@_attrs_define
class AppHealthHistoryPage:
    """Retained default HTTP health observations with independent background collection freshness."""

    app_id: str
    scope: AppHealthHistoryPageScope
    entries: list[AppHealthHistoryEntry]
    collector_fresh: bool
    """Latest background assessment exists and has not expired. This is collection freshness, not app health."""
    interval_seconds: int
    """Minimum target spacing per app; fleet load and failures can delay observations."""
    next_cursor: UUID | Unset = UNSET
    latest: AppHealthResponse | Unset = UNSET
    """Read-only observed health of default-scope HTTP serving workloads."""

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        scope: str = self.scope

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        collector_fresh = self.collector_fresh

        interval_seconds = self.interval_seconds

        next_cursor: str | Unset = UNSET
        if not isinstance(self.next_cursor, Unset):
            next_cursor = str(self.next_cursor)

        latest: dict[str, Any] | Unset = UNSET
        if not isinstance(self.latest, Unset):
            latest = self.latest.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "scope": scope,
                "entries": entries,
                "collector_fresh": collector_fresh,
                "interval_seconds": interval_seconds,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor
        if latest is not UNSET:
            field_dict["latest"] = latest

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_health_history_entry import AppHealthHistoryEntry
        from ..models.app_health_response import AppHealthResponse

        d = dict(src_dict)
        app_id = d.pop("app_id")

        scope = check_app_health_history_page_scope(d.pop("scope"))

        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = AppHealthHistoryEntry.from_dict(entries_item_data)

            entries.append(entries_item)

        collector_fresh = d.pop("collector_fresh")

        interval_seconds = d.pop("interval_seconds")

        _next_cursor = d.pop("next_cursor", UNSET)
        next_cursor: UUID | Unset
        if isinstance(_next_cursor, Unset):
            next_cursor = UNSET
        else:
            next_cursor = UUID(_next_cursor)

        _latest = d.pop("latest", UNSET)
        latest: AppHealthResponse | Unset
        if isinstance(_latest, Unset):
            latest = UNSET
        else:
            latest = AppHealthResponse.from_dict(_latest)

        app_health_history_page = cls(
            app_id=app_id,
            scope=scope,
            entries=entries,
            collector_fresh=collector_fresh,
            interval_seconds=interval_seconds,
            next_cursor=next_cursor,
            latest=latest,
        )

        return app_health_history_page
