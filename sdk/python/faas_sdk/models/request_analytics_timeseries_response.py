from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.request_analytics_timeseries_response_bucket import (
    RequestAnalyticsTimeseriesResponseBucket,
    check_request_analytics_timeseries_response_bucket,
)

if TYPE_CHECKING:
    from ..models.request_analytics_timeseries_point import RequestAnalyticsTimeseriesPoint


T = TypeVar("T", bound="RequestAnalyticsTimeseriesResponse")


@_attrs_define
class RequestAnalyticsTimeseriesResponse:
    """Zero-filled UTC hourly request analytics for
    `GET /v1/apps/{slug}/analytics/timeseries`. The effective window is
    half-open [from, until) and bounded by plan retention.

    """

    slug: str
    since: str
    """Effective series lookback after retention clamping."""
    from_: datetime.datetime
    """First instant represented by the hourly series."""
    until: datetime.datetime
    """Exclusive end instant represented by the hourly series."""
    window_clamped: bool
    """Indicates the series start was limited by plan retention."""
    bucket: RequestAnalyticsTimeseriesResponseBucket
    """UTC bucket size."""
    points: list[RequestAnalyticsTimeseriesPoint]
    as_of: datetime.datetime
    """UTC time when the series response was assembled."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        since = self.since

        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        window_clamped = self.window_clamped

        bucket: str = self.bucket

        points = []
        for points_item_data in self.points:
            points_item = points_item_data.to_dict()
            points.append(points_item)

        as_of = self.as_of.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "since": since,
                "from": from_,
                "until": until,
                "window_clamped": window_clamped,
                "bucket": bucket,
                "points": points,
                "as_of": as_of,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.request_analytics_timeseries_point import RequestAnalyticsTimeseriesPoint

        d = dict(src_dict)
        slug = d.pop("slug")

        since = d.pop("since")

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        window_clamped = d.pop("window_clamped")

        bucket = check_request_analytics_timeseries_response_bucket(d.pop("bucket"))

        points = []
        _points = d.pop("points")
        for points_item_data in _points:
            points_item = RequestAnalyticsTimeseriesPoint.from_dict(points_item_data)

            points.append(points_item)

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        request_analytics_timeseries_response = cls(
            slug=slug,
            since=since,
            from_=from_,
            until=until,
            window_clamped=window_clamped,
            bucket=bucket,
            points=points,
            as_of=as_of,
        )

        request_analytics_timeseries_response.additional_properties = d
        return request_analytics_timeseries_response

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
