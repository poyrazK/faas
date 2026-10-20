from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugCompareRequest")


@_attrs_define
class DebugCompareRequest:
    """POST body for /v1/apps/{slug}/debug/compare (ADR-127 / PR-B)."""

    source: UUID
    """Source deployment id."""
    mirror: UUID
    """Mirror deployment id."""
    route: None | str | Unset = UNSET
    """Optional exact-match route filter."""
    since: None | str | Unset = UNSET
    """Lookback duration (e.g. '24h')."""
    until: None | str | Unset = UNSET
    """Window end (RFC3339). Empty = now."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source = str(self.source)

        mirror = str(self.mirror)

        route: None | str | Unset
        if isinstance(self.route, Unset):
            route = UNSET
        else:
            route = self.route

        since: None | str | Unset
        if isinstance(self.since, Unset):
            since = UNSET
        else:
            since = self.since

        until: None | str | Unset
        if isinstance(self.until, Unset):
            until = UNSET
        else:
            until = self.until

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "mirror": mirror,
            }
        )
        if route is not UNSET:
            field_dict["route"] = route
        if since is not UNSET:
            field_dict["since"] = since
        if until is not UNSET:
            field_dict["until"] = until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source = UUID(d.pop("source"))

        mirror = UUID(d.pop("mirror"))

        def _parse_route(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        route = _parse_route(d.pop("route", UNSET))

        def _parse_since(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        since = _parse_since(d.pop("since", UNSET))

        def _parse_until(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        until = _parse_until(d.pop("until", UNSET))

        debug_compare_request = cls(
            source=source,
            mirror=mirror,
            route=route,
            since=since,
            until=until,
        )

        debug_compare_request.additional_properties = d
        return debug_compare_request

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
