from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.environment_cache_schedule_event import EnvironmentCacheScheduleEvent


T = TypeVar("T", bound="EnvironmentCacheScheduleActivity")


@_attrs_define
class EnvironmentCacheScheduleActivity:
    """Exact environment activity page with an account-bound checkpoint that can be reused after reaching the current tail."""

    app_id: UUID
    project_id: UUID
    environment_id: UUID
    scope: str
    entries: list[EnvironmentCacheScheduleEvent]
    cursor: str
    has_more: bool

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        project_id = str(self.project_id)

        environment_id = str(self.environment_id)

        scope = self.scope

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        cursor = self.cursor

        has_more = self.has_more

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "project_id": project_id,
                "environment_id": environment_id,
                "scope": scope,
                "entries": entries,
                "cursor": cursor,
                "has_more": has_more,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_cache_schedule_event import EnvironmentCacheScheduleEvent

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        project_id = UUID(d.pop("project_id"))

        environment_id = UUID(d.pop("environment_id"))

        scope = d.pop("scope")

        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = EnvironmentCacheScheduleEvent.from_dict(entries_item_data)

            entries.append(entries_item)

        cursor = d.pop("cursor")

        has_more = d.pop("has_more")

        environment_cache_schedule_activity = cls(
            app_id=app_id,
            project_id=project_id,
            environment_id=environment_id,
            scope=scope,
            entries=entries,
            cursor=cursor,
            has_more=has_more,
        )

        return environment_cache_schedule_activity
