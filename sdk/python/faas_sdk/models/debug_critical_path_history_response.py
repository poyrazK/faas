from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.debug_critical_path_history_item import DebugCriticalPathHistoryItem


T = TypeVar("T", bound="DebugCriticalPathHistoryResponse")


@_attrs_define
class DebugCriticalPathHistoryResponse:
    """Historical critical-path aggregates for one app. Raw span attributes and destinations are never exposed."""

    app_id: UUID
    since: str
    window_start: datetime.datetime
    window_end: datetime.datetime
    retention_clamped: bool
    complete: bool
    """True only when the row, path-cardinality, span-timing, and parent-link caps were not reached."""
    truncated: bool
    telemetry_rows: int
    represented_requests: int
    path_samples: int
    critical_paths: list[DebugCriticalPathHistoryItem]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        since = self.since

        window_start = self.window_start.isoformat()

        window_end = self.window_end.isoformat()

        retention_clamped = self.retention_clamped

        complete = self.complete

        truncated = self.truncated

        telemetry_rows = self.telemetry_rows

        represented_requests = self.represented_requests

        path_samples = self.path_samples

        critical_paths = []
        for critical_paths_item_data in self.critical_paths:
            critical_paths_item = critical_paths_item_data.to_dict()
            critical_paths.append(critical_paths_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "since": since,
                "window_start": window_start,
                "window_end": window_end,
                "retention_clamped": retention_clamped,
                "complete": complete,
                "truncated": truncated,
                "telemetry_rows": telemetry_rows,
                "represented_requests": represented_requests,
                "path_samples": path_samples,
                "critical_paths": critical_paths,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_critical_path_history_item import DebugCriticalPathHistoryItem

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        since = d.pop("since")

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        window_end = datetime.datetime.fromisoformat(d.pop("window_end"))

        retention_clamped = d.pop("retention_clamped")

        complete = d.pop("complete")

        truncated = d.pop("truncated")

        telemetry_rows = d.pop("telemetry_rows")

        represented_requests = d.pop("represented_requests")

        path_samples = d.pop("path_samples")

        critical_paths = []
        _critical_paths = d.pop("critical_paths")
        for critical_paths_item_data in _critical_paths:
            critical_paths_item = DebugCriticalPathHistoryItem.from_dict(critical_paths_item_data)

            critical_paths.append(critical_paths_item)

        debug_critical_path_history_response = cls(
            app_id=app_id,
            since=since,
            window_start=window_start,
            window_end=window_end,
            retention_clamped=retention_clamped,
            complete=complete,
            truncated=truncated,
            telemetry_rows=telemetry_rows,
            represented_requests=represented_requests,
            path_samples=path_samples,
            critical_paths=critical_paths,
        )

        debug_critical_path_history_response.additional_properties = d
        return debug_critical_path_history_response

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
