from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugCompareRouteStats")


@_attrs_define
class DebugCompareRouteStats:
    """Per-route latency stats for one side of the compare."""

    route: str
    source_p50_ms: int | None | Unset = UNSET
    source_p95_ms: int | None | Unset = UNSET
    source_p99_ms: int | None | Unset = UNSET
    source_count: int | None | Unset = UNSET
    mirror_p50_ms: int | None | Unset = UNSET
    mirror_p95_ms: int | None | Unset = UNSET
    mirror_p99_ms: int | None | Unset = UNSET
    mirror_count: int | None | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        route = self.route

        source_p50_ms: int | None | Unset
        if isinstance(self.source_p50_ms, Unset):
            source_p50_ms = UNSET
        else:
            source_p50_ms = self.source_p50_ms

        source_p95_ms: int | None | Unset
        if isinstance(self.source_p95_ms, Unset):
            source_p95_ms = UNSET
        else:
            source_p95_ms = self.source_p95_ms

        source_p99_ms: int | None | Unset
        if isinstance(self.source_p99_ms, Unset):
            source_p99_ms = UNSET
        else:
            source_p99_ms = self.source_p99_ms

        source_count: int | None | Unset
        if isinstance(self.source_count, Unset):
            source_count = UNSET
        else:
            source_count = self.source_count

        mirror_p50_ms: int | None | Unset
        if isinstance(self.mirror_p50_ms, Unset):
            mirror_p50_ms = UNSET
        else:
            mirror_p50_ms = self.mirror_p50_ms

        mirror_p95_ms: int | None | Unset
        if isinstance(self.mirror_p95_ms, Unset):
            mirror_p95_ms = UNSET
        else:
            mirror_p95_ms = self.mirror_p95_ms

        mirror_p99_ms: int | None | Unset
        if isinstance(self.mirror_p99_ms, Unset):
            mirror_p99_ms = UNSET
        else:
            mirror_p99_ms = self.mirror_p99_ms

        mirror_count: int | None | Unset
        if isinstance(self.mirror_count, Unset):
            mirror_count = UNSET
        else:
            mirror_count = self.mirror_count

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "route": route,
            }
        )
        if source_p50_ms is not UNSET:
            field_dict["source_p50_ms"] = source_p50_ms
        if source_p95_ms is not UNSET:
            field_dict["source_p95_ms"] = source_p95_ms
        if source_p99_ms is not UNSET:
            field_dict["source_p99_ms"] = source_p99_ms
        if source_count is not UNSET:
            field_dict["source_count"] = source_count
        if mirror_p50_ms is not UNSET:
            field_dict["mirror_p50_ms"] = mirror_p50_ms
        if mirror_p95_ms is not UNSET:
            field_dict["mirror_p95_ms"] = mirror_p95_ms
        if mirror_p99_ms is not UNSET:
            field_dict["mirror_p99_ms"] = mirror_p99_ms
        if mirror_count is not UNSET:
            field_dict["mirror_count"] = mirror_count

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        route = d.pop("route")

        def _parse_source_p50_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        source_p50_ms = _parse_source_p50_ms(d.pop("source_p50_ms", UNSET))

        def _parse_source_p95_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        source_p95_ms = _parse_source_p95_ms(d.pop("source_p95_ms", UNSET))

        def _parse_source_p99_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        source_p99_ms = _parse_source_p99_ms(d.pop("source_p99_ms", UNSET))

        def _parse_source_count(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        source_count = _parse_source_count(d.pop("source_count", UNSET))

        def _parse_mirror_p50_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        mirror_p50_ms = _parse_mirror_p50_ms(d.pop("mirror_p50_ms", UNSET))

        def _parse_mirror_p95_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        mirror_p95_ms = _parse_mirror_p95_ms(d.pop("mirror_p95_ms", UNSET))

        def _parse_mirror_p99_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        mirror_p99_ms = _parse_mirror_p99_ms(d.pop("mirror_p99_ms", UNSET))

        def _parse_mirror_count(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        mirror_count = _parse_mirror_count(d.pop("mirror_count", UNSET))

        debug_compare_route_stats = cls(
            route=route,
            source_p50_ms=source_p50_ms,
            source_p95_ms=source_p95_ms,
            source_p99_ms=source_p99_ms,
            source_count=source_count,
            mirror_p50_ms=mirror_p50_ms,
            mirror_p95_ms=mirror_p95_ms,
            mirror_p99_ms=mirror_p99_ms,
            mirror_count=mirror_count,
        )

        debug_compare_route_stats.additional_properties = d
        return debug_compare_route_stats

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
