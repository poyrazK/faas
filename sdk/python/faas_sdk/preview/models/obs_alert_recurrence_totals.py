from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.obs_alert_recurrence_metrics import ObsAlertRecurrenceMetrics


T = TypeVar("T", bound="ObsAlertRecurrenceTotals")


@_attrs_define
class ObsAlertRecurrenceTotals:
    """Complete cohort counts across distinct rule-definition and exact-series groups."""

    series: int
    repeated_series: int
    metrics: ObsAlertRecurrenceMetrics
    """Episode denominator and adjacent-repeat denominator with independent evidence partitions."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        series = self.series

        repeated_series = self.repeated_series

        metrics = self.metrics.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "series": series,
                "repeated_series": repeated_series,
                "metrics": metrics,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.obs_alert_recurrence_metrics import ObsAlertRecurrenceMetrics

        d = dict(src_dict)
        series = d.pop("series")

        repeated_series = d.pop("repeated_series")

        metrics = ObsAlertRecurrenceMetrics.from_dict(d.pop("metrics"))

        obs_alert_recurrence_totals = cls(
            series=series,
            repeated_series=repeated_series,
            metrics=metrics,
        )

        obs_alert_recurrence_totals.additional_properties = d
        return obs_alert_recurrence_totals

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
