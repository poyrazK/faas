from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DataUpstreamHistoryBucket")


@_attrs_define
class DataUpstreamHistoryBucket:
    """One server-side probe aggregation bucket. Percentiles are omitted when every probe in the bucket failed."""

    sampled_at: datetime.datetime
    """UTC start of the aggregation bucket."""
    sample_count: int
    """Total probes in the bucket, including failures."""
    p50_ms: int | None | Unset = UNSET
    """Successful probe RTT p50 in milliseconds."""
    p95_ms: int | None | Unset = UNSET
    """Successful probe RTT p95 in milliseconds."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sampled_at = self.sampled_at.isoformat()

        sample_count = self.sample_count

        p50_ms: int | None | Unset
        if isinstance(self.p50_ms, Unset):
            p50_ms = UNSET
        else:
            p50_ms = self.p50_ms

        p95_ms: int | None | Unset
        if isinstance(self.p95_ms, Unset):
            p95_ms = UNSET
        else:
            p95_ms = self.p95_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sampled_at": sampled_at,
                "sample_count": sample_count,
            }
        )
        if p50_ms is not UNSET:
            field_dict["p50_ms"] = p50_ms
        if p95_ms is not UNSET:
            field_dict["p95_ms"] = p95_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sampled_at = datetime.datetime.fromisoformat(d.pop("sampled_at"))

        sample_count = d.pop("sample_count")

        def _parse_p50_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        p50_ms = _parse_p50_ms(d.pop("p50_ms", UNSET))

        def _parse_p95_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        p95_ms = _parse_p95_ms(d.pop("p95_ms", UNSET))

        data_upstream_history_bucket = cls(
            sampled_at=sampled_at,
            sample_count=sample_count,
            p50_ms=p50_ms,
            p95_ms=p95_ms,
        )

        data_upstream_history_bucket.additional_properties = d
        return data_upstream_history_bucket

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
