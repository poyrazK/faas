from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_awake_source_source import RouteAwakeSourceSource, check_route_awake_source_source

T = TypeVar("T", bound="RouteAwakeSource")


@_attrs_define
class RouteAwakeSource:
    """Awake time one non-request cause explains (ADR-1199)."""

    source: RouteAwakeSourceSource
    """route_probe: synthetic canary probes the app answered (ADR-847; kept for a day). prewarm: instances a
    scheduled prewarm admitted."""
    events: int
    """Probe requests answered without a 5xx or an auth rejection, or prewarmed instances."""
    awake_seconds: int
    awake_share: float
    gb_hours: float
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source: str = self.source

        events = self.events

        awake_seconds = self.awake_seconds

        awake_share = self.awake_share

        gb_hours = self.gb_hours

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "events": events,
                "awake_seconds": awake_seconds,
                "awake_share": awake_share,
                "gb_hours": gb_hours,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source = check_route_awake_source_source(d.pop("source"))

        events = d.pop("events")

        awake_seconds = d.pop("awake_seconds")

        awake_share = d.pop("awake_share")

        gb_hours = d.pop("gb_hours")

        route_awake_source = cls(
            source=source,
            events=events,
            awake_seconds=awake_seconds,
            awake_share=awake_share,
            gb_hours=gb_hours,
        )

        route_awake_source.additional_properties = d
        return route_awake_source

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
