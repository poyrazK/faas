from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AlertAnomalyZeroBaselineFallback")


@_attrs_define
class AlertAnomalyZeroBaselineFallback:
    """Absolute error-rate and failure-count guard for a zero historical baseline."""

    minimum_error_rate_pct: float
    """Minimum absolute error rate expressed as a percent."""
    minimum_failed_requests: int
    """Minimum failed requests in the evaluation window."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        minimum_error_rate_pct = self.minimum_error_rate_pct

        minimum_failed_requests = self.minimum_failed_requests

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "minimum_error_rate_pct": minimum_error_rate_pct,
                "minimum_failed_requests": minimum_failed_requests,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        minimum_error_rate_pct = d.pop("minimum_error_rate_pct")

        minimum_failed_requests = d.pop("minimum_failed_requests")

        alert_anomaly_zero_baseline_fallback = cls(
            minimum_error_rate_pct=minimum_error_rate_pct,
            minimum_failed_requests=minimum_failed_requests,
        )

        alert_anomaly_zero_baseline_fallback.additional_properties = d
        return alert_anomaly_zero_baseline_fallback

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
