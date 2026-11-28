from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.insight_event_series_interval import InsightEventSeriesInterval, check_insight_event_series_interval
from ..models.insight_event_series_unit import InsightEventSeriesUnit, check_insight_event_series_unit
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.insight_event_point import InsightEventPoint


T = TypeVar("T", bound="InsightEventSeries")


@_attrs_define
class InsightEventSeries:
    """A product analytics event series."""

    event: str
    unit: InsightEventSeriesUnit
    interval: InsightEventSeriesInterval
    from_: datetime.datetime
    until: datetime.datetime
    points: list[InsightEventPoint]
    by: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event = self.event

        unit: str = self.unit

        interval: str = self.interval

        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        points = []
        for points_item_data in self.points:
            points_item = points_item_data.to_dict()
            points.append(points_item)

        by = self.by

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event": event,
                "unit": unit,
                "interval": interval,
                "from": from_,
                "until": until,
                "points": points,
            }
        )
        if by is not UNSET:
            field_dict["by"] = by

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.insight_event_point import InsightEventPoint

        d = dict(src_dict)
        event = d.pop("event")

        unit = check_insight_event_series_unit(d.pop("unit"))

        interval = check_insight_event_series_interval(d.pop("interval"))

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        points = []
        _points = d.pop("points")
        for points_item_data in _points:
            points_item = InsightEventPoint.from_dict(points_item_data)

            points.append(points_item)

        by = d.pop("by", UNSET)

        insight_event_series = cls(
            event=event,
            unit=unit,
            interval=interval,
            from_=from_,
            until=until,
            points=points,
            by=by,
        )

        insight_event_series.additional_properties = d
        return insight_event_series

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
