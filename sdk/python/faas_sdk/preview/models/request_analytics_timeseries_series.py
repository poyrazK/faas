from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.request_analytics_timeseries_series_method import (
    RequestAnalyticsTimeseriesSeriesMethod,
    check_request_analytics_timeseries_series_method,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.request_analytics_timeseries_point import RequestAnalyticsTimeseriesPoint


T = TypeVar("T", bound="RequestAnalyticsTimeseriesSeries")


@_attrs_define
class RequestAnalyticsTimeseriesSeries:
    """A grouped request analytics series with its hourly points."""

    value: str
    points: list[RequestAnalyticsTimeseriesPoint]
    method: RequestAnalyticsTimeseriesSeriesMethod | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        value = self.value

        points = []
        for points_item_data in self.points:
            points_item = points_item_data.to_dict()
            points.append(points_item)

        method: str | Unset = UNSET
        if not isinstance(self.method, Unset):
            method = self.method

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "value": value,
                "points": points,
            }
        )
        if method is not UNSET:
            field_dict["method"] = method

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.request_analytics_timeseries_point import RequestAnalyticsTimeseriesPoint

        d = dict(src_dict)
        value = d.pop("value")

        points = []
        _points = d.pop("points")
        for points_item_data in _points:
            points_item = RequestAnalyticsTimeseriesPoint.from_dict(points_item_data)

            points.append(points_item)

        _method = d.pop("method", UNSET)
        method: RequestAnalyticsTimeseriesSeriesMethod | Unset
        if isinstance(_method, Unset):
            method = UNSET
        else:
            method = check_request_analytics_timeseries_series_method(_method)

        request_analytics_timeseries_series = cls(
            value=value,
            points=points,
            method=method,
        )

        request_analytics_timeseries_series.additional_properties = d
        return request_analytics_timeseries_series

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
