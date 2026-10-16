from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.environment_cache_schedule import EnvironmentCacheSchedule


T = TypeVar("T", bound="EnvironmentCacheScheduleList")


@_attrs_define
class EnvironmentCacheScheduleList:
    """Scoped live schedule page ordered by execution timestamp and schedule UUID, with filter-bound continuation."""

    app_id: UUID
    project_id: UUID
    environment_id: UUID
    scope: str
    entries: list[EnvironmentCacheSchedule]
    next_cursor: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        project_id = str(self.project_id)

        environment_id = str(self.environment_id)

        scope = self.scope

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "project_id": project_id,
                "environment_id": environment_id,
                "scope": scope,
                "entries": entries,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_cache_schedule import EnvironmentCacheSchedule

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        project_id = UUID(d.pop("project_id"))

        environment_id = UUID(d.pop("environment_id"))

        scope = d.pop("scope")

        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = EnvironmentCacheSchedule.from_dict(entries_item_data)

            entries.append(entries_item)

        next_cursor = d.pop("next_cursor", UNSET)

        environment_cache_schedule_list = cls(
            app_id=app_id,
            project_id=project_id,
            environment_id=environment_id,
            scope=scope,
            entries=entries,
            next_cursor=next_cursor,
        )

        return environment_cache_schedule_list
