from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.debug_telemetry_request_item import DebugTelemetryRequestItem


T = TypeVar("T", bound="DebugTelemetryListResponse")


@_attrs_define
class DebugTelemetryListResponse:
    """Response from GET /v1/apps/{slug}/debug/requests (ADR-127).
    `since` echoes the effective window used (after the plan's
    `DebugTelemetryRetentionDays` clamp) so the dashboard can
    surface a "you widened past the cap" tile.

    """

    since: str
    """Effective window applied (e.g. '24h', '72h')."""
    window_start: datetime.datetime
    """Inclusive start of the pinned retention window."""
    window_end: datetime.datetime
    """Exclusive end of the pinned retention window."""
    retention_clamped: bool
    """True when the requested lookback exceeded the plan retention cap."""
    complete: bool
    """True when this page contains every retained row in the pinned window."""
    requests: list[DebugTelemetryRequestItem]
    next_cursor: str | Unset = UNSET
    """Opaque cursor for the next page; omitted when complete is true."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        since = self.since

        window_start = self.window_start.isoformat()

        window_end = self.window_end.isoformat()

        retention_clamped = self.retention_clamped

        complete = self.complete

        requests = []
        for requests_item_data in self.requests:
            requests_item = requests_item_data.to_dict()
            requests.append(requests_item)

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "since": since,
                "window_start": window_start,
                "window_end": window_end,
                "retention_clamped": retention_clamped,
                "complete": complete,
                "requests": requests,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_telemetry_request_item import DebugTelemetryRequestItem

        d = dict(src_dict)
        since = d.pop("since")

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        window_end = datetime.datetime.fromisoformat(d.pop("window_end"))

        retention_clamped = d.pop("retention_clamped")

        complete = d.pop("complete")

        requests = []
        _requests = d.pop("requests")
        for requests_item_data in _requests:
            requests_item = DebugTelemetryRequestItem.from_dict(requests_item_data)

            requests.append(requests_item)

        next_cursor = d.pop("next_cursor", UNSET)

        debug_telemetry_list_response = cls(
            since=since,
            window_start=window_start,
            window_end=window_end,
            retention_clamped=retention_clamped,
            complete=complete,
            requests=requests,
            next_cursor=next_cursor,
        )

        debug_telemetry_list_response.additional_properties = d
        return debug_telemetry_list_response

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
