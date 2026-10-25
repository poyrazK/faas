from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.custom_metric_response import CustomMetricResponse


T = TypeVar("T", bound="CustomMetricListResponse")


@_attrs_define
class CustomMetricListResponse:
    """The app's ADR-202 gauges, plus the two limits needed to interpret them so debugging does not require reading the
    docs.

    """

    metrics: list[CustomMetricResponse]
    freshness_seconds: int
    """A metric older than this reports no signal to the scheduler rather than its last value, so a dead pusher
    cannot pin the fleet at a frozen backlog."""
    max_metrics: int
    """Maximum distinct metric names this app may hold."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        metrics = []
        for metrics_item_data in self.metrics:
            metrics_item = metrics_item_data.to_dict()
            metrics.append(metrics_item)

        freshness_seconds = self.freshness_seconds

        max_metrics = self.max_metrics

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "metrics": metrics,
                "freshness_seconds": freshness_seconds,
                "max_metrics": max_metrics,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.custom_metric_response import CustomMetricResponse

        d = dict(src_dict)
        metrics = []
        _metrics = d.pop("metrics")
        for metrics_item_data in _metrics:
            metrics_item = CustomMetricResponse.from_dict(metrics_item_data)

            metrics.append(metrics_item)

        freshness_seconds = d.pop("freshness_seconds")

        max_metrics = d.pop("max_metrics")

        custom_metric_list_response = cls(
            metrics=metrics,
            freshness_seconds=freshness_seconds,
            max_metrics=max_metrics,
        )

        custom_metric_list_response.additional_properties = d
        return custom_metric_list_response

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
