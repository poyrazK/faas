from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_awake_source_pattern import RouteAwakeSourcePattern, check_route_awake_source_pattern
from ..models.route_awake_source_source import RouteAwakeSourceSource, check_route_awake_source_source
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_awake_suggestion import RouteAwakeSuggestion


T = TypeVar("T", bound="RouteAwakeSource")


@_attrs_define
class RouteAwakeSource:
    """Awake time one non-request cause explains (ADR-1199, ADR-20261022184757)."""

    source: RouteAwakeSourceSource
    """route_probe: synthetic canary probes the app answered (ADR-847; kept for a day). prewarm: instances a
    scheduled prewarm admitted. The others are deliveries schedd made to the app, by invocation source; a delivery
    counts from when it reached the instance until it completed, at most 60 minutes."""
    events: int
    """Probe requests answered without a 5xx or an auth rejection, prewarmed instances, or deliveries that started
    in the window."""
    awake_seconds: int
    awake_share: float
    gb_hours: float
    name: str | Unset = UNSET
    """One cron job (the path or command it runs and its schedule) or one queue; absent for other sources."""
    pattern: RouteAwakeSourcePattern | Unset = UNSET
    """Set for cron jobs: periodic when the job fires at a steady interval."""
    interval_seconds: int | Unset = UNSET
    """Approximate interval between the cron job's firings when pattern is periodic."""
    suggestion: RouteAwakeSuggestion | Unset = UNSET
    """A change that would let the app park."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source: str = self.source

        events = self.events

        awake_seconds = self.awake_seconds

        awake_share = self.awake_share

        gb_hours = self.gb_hours

        name = self.name

        pattern: str | Unset = UNSET
        if not isinstance(self.pattern, Unset):
            pattern = self.pattern

        interval_seconds = self.interval_seconds

        suggestion: dict[str, Any] | Unset = UNSET
        if not isinstance(self.suggestion, Unset):
            suggestion = self.suggestion.to_dict()

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
        if name is not UNSET:
            field_dict["name"] = name
        if pattern is not UNSET:
            field_dict["pattern"] = pattern
        if interval_seconds is not UNSET:
            field_dict["interval_seconds"] = interval_seconds
        if suggestion is not UNSET:
            field_dict["suggestion"] = suggestion

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_awake_suggestion import RouteAwakeSuggestion

        d = dict(src_dict)
        source = check_route_awake_source_source(d.pop("source"))

        events = d.pop("events")

        awake_seconds = d.pop("awake_seconds")

        awake_share = d.pop("awake_share")

        gb_hours = d.pop("gb_hours")

        name = d.pop("name", UNSET)

        _pattern = d.pop("pattern", UNSET)
        pattern: RouteAwakeSourcePattern | Unset
        if isinstance(_pattern, Unset):
            pattern = UNSET
        else:
            pattern = check_route_awake_source_pattern(_pattern)

        interval_seconds = d.pop("interval_seconds", UNSET)

        _suggestion = d.pop("suggestion", UNSET)
        suggestion: RouteAwakeSuggestion | Unset
        if isinstance(_suggestion, Unset):
            suggestion = UNSET
        else:
            suggestion = RouteAwakeSuggestion.from_dict(_suggestion)

        route_awake_source = cls(
            source=source,
            events=events,
            awake_seconds=awake_seconds,
            awake_share=awake_share,
            gb_hours=gb_hours,
            name=name,
            pattern=pattern,
            interval_seconds=interval_seconds,
            suggestion=suggestion,
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
