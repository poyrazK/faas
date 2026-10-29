from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_wake_history_page_count_scope import (
    AppWakeHistoryPageCountScope,
    check_app_wake_history_page_count_scope,
)
from ..models.app_wake_history_page_mode import AppWakeHistoryPageMode, check_app_wake_history_page_mode
from ..models.app_wake_history_page_source_status import (
    AppWakeHistoryPageSourceStatus,
    check_app_wake_history_page_source_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AppWakeHistoryPage")


@_attrs_define
class AppWakeHistoryPage:
    """Live retained instance-history page. App/window filtering precedes the bound; missing start times are excluded.
    Counts describe this page only. Source success does not prove capture completeness, freshness or a snapshot.
    Instance IDs stay in opaque cursors.

    """

    mode: AppWakeHistoryPageMode
    since: datetime.datetime
    until: datetime.datetime
    limit: int
    complete: bool
    source_status: AppWakeHistoryPageSourceStatus
    count_scope: AppWakeHistoryPageCountScope
    cursor: str | Unset = UNSET
    next_cursor: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        since = self.since.isoformat()

        until = self.until.isoformat()

        limit = self.limit

        complete = self.complete

        source_status: str = self.source_status

        count_scope: str = self.count_scope

        cursor = self.cursor

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "mode": mode,
                "since": since,
                "until": until,
                "limit": limit,
                "complete": complete,
                "source_status": source_status,
                "count_scope": count_scope,
            }
        )
        if cursor is not UNSET:
            field_dict["cursor"] = cursor
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_app_wake_history_page_mode(d.pop("mode"))

        since = datetime.datetime.fromisoformat(d.pop("since"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        limit = d.pop("limit")

        complete = d.pop("complete")

        source_status = check_app_wake_history_page_source_status(d.pop("source_status"))

        count_scope = check_app_wake_history_page_count_scope(d.pop("count_scope"))

        cursor = d.pop("cursor", UNSET)

        next_cursor = d.pop("next_cursor", UNSET)

        app_wake_history_page = cls(
            mode=mode,
            since=since,
            until=until,
            limit=limit,
            complete=complete,
            source_status=source_status,
            count_scope=count_scope,
            cursor=cursor,
            next_cursor=next_cursor,
        )

        app_wake_history_page.additional_properties = d
        return app_wake_history_page

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
