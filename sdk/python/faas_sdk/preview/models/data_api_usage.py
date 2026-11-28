from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.data_api_usage_granularity import DataAPIUsageGranularity, check_data_api_usage_granularity

if TYPE_CHECKING:
    from ..models.data_api_usage_bucket import DataAPIUsageBucket
    from ..models.data_api_usage_counts import DataAPIUsageCounts
    from ..models.data_api_usage_month_to_date import DataAPIUsageMonthToDate


T = TypeVar("T", bound="DataAPIUsage")


@_attrs_define
class DataAPIUsage:
    """An app's reported Data API usage over a window, zero-filled per bucket."""

    app: str
    from_: datetime.datetime
    to: datetime.datetime
    granularity: DataAPIUsageGranularity
    totals: DataAPIUsageCounts
    """Data API usage counts. Admitted requests by access kind, gateway and engine rejections (4xx), errors (5xx),
    requests refused by owner caps, rows the engine reported and response bytes. Owner telemetry from the app's
    runtime, not billing records."""
    month_to_date: DataAPIUsageMonthToDate
    """Admitted requests in the current UTC calendar month, all and anonymous, which owner caps compare against."""
    buckets: list[DataAPIUsageBucket]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        from_ = self.from_.isoformat()

        to = self.to.isoformat()

        granularity: str = self.granularity

        totals = self.totals.to_dict()

        month_to_date = self.month_to_date.to_dict()

        buckets = []
        for buckets_item_data in self.buckets:
            buckets_item = buckets_item_data.to_dict()
            buckets.append(buckets_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "from": from_,
                "to": to,
                "granularity": granularity,
                "totals": totals,
                "month_to_date": month_to_date,
                "buckets": buckets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_api_usage_bucket import DataAPIUsageBucket
        from ..models.data_api_usage_counts import DataAPIUsageCounts
        from ..models.data_api_usage_month_to_date import DataAPIUsageMonthToDate

        d = dict(src_dict)
        app = d.pop("app")

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        to = datetime.datetime.fromisoformat(d.pop("to"))

        granularity = check_data_api_usage_granularity(d.pop("granularity"))

        totals = DataAPIUsageCounts.from_dict(d.pop("totals"))

        month_to_date = DataAPIUsageMonthToDate.from_dict(d.pop("month_to_date"))

        buckets = []
        _buckets = d.pop("buckets")
        for buckets_item_data in _buckets:
            buckets_item = DataAPIUsageBucket.from_dict(buckets_item_data)

            buckets.append(buckets_item)

        data_api_usage = cls(
            app=app,
            from_=from_,
            to=to,
            granularity=granularity,
            totals=totals,
            month_to_date=month_to_date,
            buckets=buckets,
        )

        data_api_usage.additional_properties = d
        return data_api_usage

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
