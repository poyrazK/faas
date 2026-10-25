from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_awake_route_pattern import RouteAwakeRoutePattern, check_route_awake_route_pattern
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_awake_suggestion import RouteAwakeSuggestion
    from ..models.route_awake_what_if import RouteAwakeWhatIf


T = TypeVar("T", bound="RouteAwakeRoute")


@_attrs_define
class RouteAwakeRoute:
    """One route's share of the app's awake time and what removing its requests would change."""

    method: str
    route: str
    """Route template, for example /orders/{id}."""
    requests: int
    """Requests that restarted the idle timer."""
    wakes: int
    """Cold boots this route's requests triggered."""
    awake_seconds: int
    awake_share: float
    gb_hours: float
    pattern: RouteAwakeRoutePattern
    if_removed: RouteAwakeWhatIf
    """Estimated change had this route's requests never reached the app, with every other request unchanged."""
    interval_seconds: int | Unset = UNSET
    """Approximate interval when pattern is periodic."""
    suggestion: RouteAwakeSuggestion | Unset = UNSET
    """A change that would let the app park."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method = self.method

        route = self.route

        requests = self.requests

        wakes = self.wakes

        awake_seconds = self.awake_seconds

        awake_share = self.awake_share

        gb_hours = self.gb_hours

        pattern: str = self.pattern

        if_removed = self.if_removed.to_dict()

        interval_seconds = self.interval_seconds

        suggestion: dict[str, Any] | Unset = UNSET
        if not isinstance(self.suggestion, Unset):
            suggestion = self.suggestion.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "route": route,
                "requests": requests,
                "wakes": wakes,
                "awake_seconds": awake_seconds,
                "awake_share": awake_share,
                "gb_hours": gb_hours,
                "pattern": pattern,
                "if_removed": if_removed,
            }
        )
        if interval_seconds is not UNSET:
            field_dict["interval_seconds"] = interval_seconds
        if suggestion is not UNSET:
            field_dict["suggestion"] = suggestion

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_awake_suggestion import RouteAwakeSuggestion
        from ..models.route_awake_what_if import RouteAwakeWhatIf

        d = dict(src_dict)
        method = d.pop("method")

        route = d.pop("route")

        requests = d.pop("requests")

        wakes = d.pop("wakes")

        awake_seconds = d.pop("awake_seconds")

        awake_share = d.pop("awake_share")

        gb_hours = d.pop("gb_hours")

        pattern = check_route_awake_route_pattern(d.pop("pattern"))

        if_removed = RouteAwakeWhatIf.from_dict(d.pop("if_removed"))

        interval_seconds = d.pop("interval_seconds", UNSET)

        _suggestion = d.pop("suggestion", UNSET)
        suggestion: RouteAwakeSuggestion | Unset
        if isinstance(_suggestion, Unset):
            suggestion = UNSET
        else:
            suggestion = RouteAwakeSuggestion.from_dict(_suggestion)

        route_awake_route = cls(
            method=method,
            route=route,
            requests=requests,
            wakes=wakes,
            awake_seconds=awake_seconds,
            awake_share=awake_share,
            gb_hours=gb_hours,
            pattern=pattern,
            if_removed=if_removed,
            interval_seconds=interval_seconds,
            suggestion=suggestion,
        )

        route_awake_route.additional_properties = d
        return route_awake_route

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
