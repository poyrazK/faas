from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="MirrorSummaryResponse")


@_attrs_define
class MirrorSummaryResponse:
    """Aggregated mirror drift counts over a trailing window. PR-A2
    returns zeros (PR-A1's ledger has no writers until A3 ships
    the runtime dispatch); post-A3 this is the dashboard widget's
    data source.

    """

    total_invocations: int
    changed_response_count: int
    """Requests with any status, schema, or body difference; each request is counted once."""
    changed_response_percent: float
    """100 × changed_response_count / total_invocations; zero when the window is empty."""
    status_diff_count: int
    schema_diff_count: int
    body_diff_count: int
    mean_latency_diff_ms: int
    """Signed: mirror - source. Positive = mirror slower."""
    p99_latency_diff_ms: int
    crash_count: int
    window_seconds: int
    """The window's length in seconds. Matches the requested `?window=` value."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        total_invocations = self.total_invocations

        changed_response_count = self.changed_response_count

        changed_response_percent = self.changed_response_percent

        status_diff_count = self.status_diff_count

        schema_diff_count = self.schema_diff_count

        body_diff_count = self.body_diff_count

        mean_latency_diff_ms = self.mean_latency_diff_ms

        p99_latency_diff_ms = self.p99_latency_diff_ms

        crash_count = self.crash_count

        window_seconds = self.window_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "total_invocations": total_invocations,
                "changed_response_count": changed_response_count,
                "changed_response_percent": changed_response_percent,
                "status_diff_count": status_diff_count,
                "schema_diff_count": schema_diff_count,
                "body_diff_count": body_diff_count,
                "mean_latency_diff_ms": mean_latency_diff_ms,
                "p99_latency_diff_ms": p99_latency_diff_ms,
                "crash_count": crash_count,
                "window_seconds": window_seconds,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        total_invocations = d.pop("total_invocations")

        changed_response_count = d.pop("changed_response_count")

        changed_response_percent = d.pop("changed_response_percent")

        status_diff_count = d.pop("status_diff_count")

        schema_diff_count = d.pop("schema_diff_count")

        body_diff_count = d.pop("body_diff_count")

        mean_latency_diff_ms = d.pop("mean_latency_diff_ms")

        p99_latency_diff_ms = d.pop("p99_latency_diff_ms")

        crash_count = d.pop("crash_count")

        window_seconds = d.pop("window_seconds")

        mirror_summary_response = cls(
            total_invocations=total_invocations,
            changed_response_count=changed_response_count,
            changed_response_percent=changed_response_percent,
            status_diff_count=status_diff_count,
            schema_diff_count=schema_diff_count,
            body_diff_count=body_diff_count,
            mean_latency_diff_ms=mean_latency_diff_ms,
            p99_latency_diff_ms=p99_latency_diff_ms,
            crash_count=crash_count,
            window_seconds=window_seconds,
        )

        mirror_summary_response.additional_properties = d
        return mirror_summary_response

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
