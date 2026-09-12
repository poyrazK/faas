from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.status_incident import StatusIncident
    from ..models.status_uptime_bucket import StatusUptimeBucket


T = TypeVar("T", bound="StatusPage")


@_attrs_define
class StatusPage:
    """Backwards-compatible three-indicator status response."""

    api_availability_pct: float
    wake_p95_ms: float
    build_success_pct: float
    uptime_30d_pct: float
    uptime_30d: list[StatusUptimeBucket]
    incidents: list[StatusIncident]
    degraded: bool
    as_of: datetime.datetime
    source: str

    def to_dict(self) -> dict[str, Any]:
        api_availability_pct = self.api_availability_pct

        wake_p95_ms = self.wake_p95_ms

        build_success_pct = self.build_success_pct

        uptime_30d_pct = self.uptime_30d_pct

        uptime_30d = []
        for uptime_30d_item_data in self.uptime_30d:
            uptime_30d_item = uptime_30d_item_data.to_dict()
            uptime_30d.append(uptime_30d_item)

        incidents = []
        for incidents_item_data in self.incidents:
            incidents_item = incidents_item_data.to_dict()
            incidents.append(incidents_item)

        degraded = self.degraded

        as_of = self.as_of.isoformat()

        source = self.source

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "api_availability_pct": api_availability_pct,
                "wake_p95_ms": wake_p95_ms,
                "build_success_pct": build_success_pct,
                "uptime_30d_pct": uptime_30d_pct,
                "uptime_30d": uptime_30d,
                "incidents": incidents,
                "degraded": degraded,
                "as_of": as_of,
                "source": source,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.status_incident import StatusIncident
        from ..models.status_uptime_bucket import StatusUptimeBucket

        d = dict(src_dict)
        api_availability_pct = d.pop("api_availability_pct")

        wake_p95_ms = d.pop("wake_p95_ms")

        build_success_pct = d.pop("build_success_pct")

        uptime_30d_pct = d.pop("uptime_30d_pct")

        uptime_30d = []
        _uptime_30d = d.pop("uptime_30d")
        for uptime_30d_item_data in _uptime_30d:
            uptime_30d_item = StatusUptimeBucket.from_dict(uptime_30d_item_data)

            uptime_30d.append(uptime_30d_item)

        incidents = []
        _incidents = d.pop("incidents")
        for incidents_item_data in _incidents:
            incidents_item = StatusIncident.from_dict(incidents_item_data)

            incidents.append(incidents_item)

        degraded = d.pop("degraded")

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        source = d.pop("source")

        status_page = cls(
            api_availability_pct=api_availability_pct,
            wake_p95_ms=wake_p95_ms,
            build_success_pct=build_success_pct,
            uptime_30d_pct=uptime_30d_pct,
            uptime_30d=uptime_30d,
            incidents=incidents,
            degraded=degraded,
            as_of=as_of,
            source=source,
        )

        return status_page
