from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.custom_metric_series_value_labels import CustomMetricSeriesValueLabels


T = TypeVar("T", bound="CustomMetricSeriesValue")


@_attrs_define
class CustomMetricSeriesValue:
    """One labelled series of a custom metric (from OTLP data point attributes)."""

    key: str
    """Canonical k=v,... form, keys sorted; pass it as labels to the history endpoint."""
    labels: CustomMetricSeriesValueLabels
    value: float
    observed_at: datetime.datetime
    stale: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        labels = self.labels.to_dict()

        value = self.value

        observed_at = self.observed_at.isoformat()

        stale = self.stale

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "labels": labels,
                "value": value,
                "observed_at": observed_at,
                "stale": stale,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.custom_metric_series_value_labels import CustomMetricSeriesValueLabels

        d = dict(src_dict)
        key = d.pop("key")

        labels = CustomMetricSeriesValueLabels.from_dict(d.pop("labels"))

        value = d.pop("value")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        stale = d.pop("stale")

        custom_metric_series_value = cls(
            key=key,
            labels=labels,
            value=value,
            observed_at=observed_at,
            stale=stale,
        )

        custom_metric_series_value.additional_properties = d
        return custom_metric_series_value

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
