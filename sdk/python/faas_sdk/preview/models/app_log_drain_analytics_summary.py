from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AppLogDrainAnalyticsSummary")


@_attrs_define
class AppLogDrainAnalyticsSummary:
    """Aggregate customer-safe delivery analytics over the requested window."""

    delivered: int
    failed: int
    dropped: int
    retries: int
    dead_letters: int
    success_rate: float
    average_latency_ms: float
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        delivered = self.delivered

        failed = self.failed

        dropped = self.dropped

        retries = self.retries

        dead_letters = self.dead_letters

        success_rate = self.success_rate

        average_latency_ms = self.average_latency_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "delivered": delivered,
                "failed": failed,
                "dropped": dropped,
                "retries": retries,
                "dead_letters": dead_letters,
                "success_rate": success_rate,
                "average_latency_ms": average_latency_ms,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        delivered = d.pop("delivered")

        failed = d.pop("failed")

        dropped = d.pop("dropped")

        retries = d.pop("retries")

        dead_letters = d.pop("dead_letters")

        success_rate = d.pop("success_rate")

        average_latency_ms = d.pop("average_latency_ms")

        app_log_drain_analytics_summary = cls(
            delivered=delivered,
            failed=failed,
            dropped=dropped,
            retries=retries,
            dead_letters=dead_letters,
            success_rate=success_rate,
            average_latency_ms=average_latency_ms,
        )

        app_log_drain_analytics_summary.additional_properties = d
        return app_log_drain_analytics_summary

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
