from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_log_drain_analytics_response_bucket_interval import (
    AppLogDrainAnalyticsResponseBucketInterval,
    check_app_log_drain_analytics_response_bucket_interval,
)
from ..models.app_log_drain_analytics_response_window import (
    AppLogDrainAnalyticsResponseWindow,
    check_app_log_drain_analytics_response_window,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_log_drain_analytics_bucket import AppLogDrainAnalyticsBucket
    from ..models.app_log_drain_analytics_summary import AppLogDrainAnalyticsSummary


T = TypeVar("T", bound="AppLogDrainAnalyticsResponse")


@_attrs_define
class AppLogDrainAnalyticsResponse:
    """Bounded hourly, customer-safe delivery analytics for one runtime log destination."""

    log_drain_id: str
    window: AppLogDrainAnalyticsResponseWindow
    bucket_interval: AppLogDrainAnalyticsResponseBucketInterval
    from_: datetime.datetime
    to: datetime.datetime
    buckets: list[AppLogDrainAnalyticsBucket]
    summary: AppLogDrainAnalyticsSummary
    """Aggregate customer-safe delivery analytics over the requested window."""
    requested_from: datetime.datetime | Unset = UNSET
    """Original requested lower bound before expanding to the recorded hourly observations."""
    window_aligned: bool | Unset = UNSET
    """True when hourly observations changed the effective bounds; from/to match the counter observation times."""
    complete: bool | Unset = UNSET
    """Whether every reported counter delta has a valid baseline without missing hourly samples."""
    coverage_gaps: list[str] | Unset = UNSET
    """Missing baseline or hourly sample evidence; unknown deltas are omitted from totals."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        log_drain_id = self.log_drain_id

        window: str = self.window

        bucket_interval: str = self.bucket_interval

        from_ = self.from_.isoformat()

        to = self.to.isoformat()

        buckets = []
        for buckets_item_data in self.buckets:
            buckets_item = buckets_item_data.to_dict()
            buckets.append(buckets_item)

        summary = self.summary.to_dict()

        requested_from: str | Unset = UNSET
        if not isinstance(self.requested_from, Unset):
            requested_from = self.requested_from.isoformat()

        window_aligned = self.window_aligned

        complete = self.complete

        coverage_gaps: list[str] | Unset = UNSET
        if not isinstance(self.coverage_gaps, Unset):
            coverage_gaps = self.coverage_gaps

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "log_drain_id": log_drain_id,
                "window": window,
                "bucket_interval": bucket_interval,
                "from": from_,
                "to": to,
                "buckets": buckets,
                "summary": summary,
            }
        )
        if requested_from is not UNSET:
            field_dict["requested_from"] = requested_from
        if window_aligned is not UNSET:
            field_dict["window_aligned"] = window_aligned
        if complete is not UNSET:
            field_dict["complete"] = complete
        if coverage_gaps is not UNSET:
            field_dict["coverage_gaps"] = coverage_gaps

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_log_drain_analytics_bucket import AppLogDrainAnalyticsBucket
        from ..models.app_log_drain_analytics_summary import AppLogDrainAnalyticsSummary

        d = dict(src_dict)
        log_drain_id = d.pop("log_drain_id")

        window = check_app_log_drain_analytics_response_window(d.pop("window"))

        bucket_interval = check_app_log_drain_analytics_response_bucket_interval(d.pop("bucket_interval"))

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        to = datetime.datetime.fromisoformat(d.pop("to"))

        buckets = []
        _buckets = d.pop("buckets")
        for buckets_item_data in _buckets:
            buckets_item = AppLogDrainAnalyticsBucket.from_dict(buckets_item_data)

            buckets.append(buckets_item)

        summary = AppLogDrainAnalyticsSummary.from_dict(d.pop("summary"))

        _requested_from = d.pop("requested_from", UNSET)
        requested_from: datetime.datetime | Unset
        if isinstance(_requested_from, Unset):
            requested_from = UNSET
        else:
            requested_from = datetime.datetime.fromisoformat(_requested_from)

        window_aligned = d.pop("window_aligned", UNSET)

        complete = d.pop("complete", UNSET)

        coverage_gaps = cast(list[str], d.pop("coverage_gaps", UNSET))

        app_log_drain_analytics_response = cls(
            log_drain_id=log_drain_id,
            window=window,
            bucket_interval=bucket_interval,
            from_=from_,
            to=to,
            buckets=buckets,
            summary=summary,
            requested_from=requested_from,
            window_aligned=window_aligned,
            complete=complete,
            coverage_gaps=coverage_gaps,
        )

        app_log_drain_analytics_response.additional_properties = d
        return app_log_drain_analytics_response

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
