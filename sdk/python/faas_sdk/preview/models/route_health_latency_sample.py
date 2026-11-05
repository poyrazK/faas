from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RouteHealthLatencySample")


@_attrs_define
class RouteHealthLatencySample:
    """Retained row counts and publisher weights. Span samples are actual retained spans, at most 100 per row. Guest p95
    weights measured rows, including zero. Wake boot p95 counts each distinct wake once and requires scheduler events
    scoped to the app and recorded instance on the selected deployment. Missing stage evidence is omitted; incomplete
    timing suppresses exclusive percentiles.

    """

    sampled_rows: int
    sampled_requests: int
    samples_truncated: bool
    span_rows: int
    missing_span_rows: int
    span_samples: int
    spans_truncated: bool
    timing_incomplete: bool
    guest_rows: int
    guest_requests: int
    cold_boot_requests: int
    wake_samples: int
    guest_p95_ms: int | Unset = UNSET
    wake_boot_p95_ms: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sampled_rows = self.sampled_rows

        sampled_requests = self.sampled_requests

        samples_truncated = self.samples_truncated

        span_rows = self.span_rows

        missing_span_rows = self.missing_span_rows

        span_samples = self.span_samples

        spans_truncated = self.spans_truncated

        timing_incomplete = self.timing_incomplete

        guest_rows = self.guest_rows

        guest_requests = self.guest_requests

        cold_boot_requests = self.cold_boot_requests

        wake_samples = self.wake_samples

        guest_p95_ms = self.guest_p95_ms

        wake_boot_p95_ms = self.wake_boot_p95_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sampled_rows": sampled_rows,
                "sampled_requests": sampled_requests,
                "samples_truncated": samples_truncated,
                "span_rows": span_rows,
                "missing_span_rows": missing_span_rows,
                "span_samples": span_samples,
                "spans_truncated": spans_truncated,
                "timing_incomplete": timing_incomplete,
                "guest_rows": guest_rows,
                "guest_requests": guest_requests,
                "cold_boot_requests": cold_boot_requests,
                "wake_samples": wake_samples,
            }
        )
        if guest_p95_ms is not UNSET:
            field_dict["guest_p95_ms"] = guest_p95_ms
        if wake_boot_p95_ms is not UNSET:
            field_dict["wake_boot_p95_ms"] = wake_boot_p95_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sampled_rows = d.pop("sampled_rows")

        sampled_requests = d.pop("sampled_requests")

        samples_truncated = d.pop("samples_truncated")

        span_rows = d.pop("span_rows")

        missing_span_rows = d.pop("missing_span_rows")

        span_samples = d.pop("span_samples")

        spans_truncated = d.pop("spans_truncated")

        timing_incomplete = d.pop("timing_incomplete")

        guest_rows = d.pop("guest_rows")

        guest_requests = d.pop("guest_requests")

        cold_boot_requests = d.pop("cold_boot_requests")

        wake_samples = d.pop("wake_samples")

        guest_p95_ms = d.pop("guest_p95_ms", UNSET)

        wake_boot_p95_ms = d.pop("wake_boot_p95_ms", UNSET)

        route_health_latency_sample = cls(
            sampled_rows=sampled_rows,
            sampled_requests=sampled_requests,
            samples_truncated=samples_truncated,
            span_rows=span_rows,
            missing_span_rows=missing_span_rows,
            span_samples=span_samples,
            spans_truncated=spans_truncated,
            timing_incomplete=timing_incomplete,
            guest_rows=guest_rows,
            guest_requests=guest_requests,
            cold_boot_requests=cold_boot_requests,
            wake_samples=wake_samples,
            guest_p95_ms=guest_p95_ms,
            wake_boot_p95_ms=wake_boot_p95_ms,
        )

        route_health_latency_sample.additional_properties = d
        return route_health_latency_sample

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
