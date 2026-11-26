from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.insight_events_query_interval import InsightEventsQueryInterval, check_insight_events_query_interval
from ..models.insight_events_query_measure import InsightEventsQueryMeasure, check_insight_events_query_measure
from ..models.insight_events_query_unit import InsightEventsQueryUnit, check_insight_events_query_unit
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.insight_events_query_where import InsightEventsQueryWhere


T = TypeVar("T", bound="InsightEventsQuery")


@_attrs_define
class InsightEventsQuery:
    """A saved events insight: the event series query and the measure its value totals."""

    event: str
    where: InsightEventsQueryWhere | Unset = UNSET
    interval: InsightEventsQueryInterval | Unset = "day"
    unit: InsightEventsQueryUnit | Unset = "customer"
    by: str | Unset = UNSET
    """Breakdown of the series: flag:KEY or property:KEY."""
    measure: InsightEventsQueryMeasure | Unset = "events"
    """What the insight's value totals."""

    def to_dict(self) -> dict[str, Any]:
        event = self.event

        where: dict[str, Any] | Unset = UNSET
        if not isinstance(self.where, Unset):
            where = self.where.to_dict()

        interval: str | Unset = UNSET
        if not isinstance(self.interval, Unset):
            interval = self.interval

        unit: str | Unset = UNSET
        if not isinstance(self.unit, Unset):
            unit = self.unit

        by = self.by

        measure: str | Unset = UNSET
        if not isinstance(self.measure, Unset):
            measure = self.measure

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event": event,
            }
        )
        if where is not UNSET:
            field_dict["where"] = where
        if interval is not UNSET:
            field_dict["interval"] = interval
        if unit is not UNSET:
            field_dict["unit"] = unit
        if by is not UNSET:
            field_dict["by"] = by
        if measure is not UNSET:
            field_dict["measure"] = measure

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.insight_events_query_where import InsightEventsQueryWhere

        d = dict(src_dict)
        event = d.pop("event")

        _where = d.pop("where", UNSET)
        where: InsightEventsQueryWhere | Unset
        if isinstance(_where, Unset):
            where = UNSET
        else:
            where = InsightEventsQueryWhere.from_dict(_where)

        _interval = d.pop("interval", UNSET)
        interval: InsightEventsQueryInterval | Unset
        if isinstance(_interval, Unset):
            interval = UNSET
        else:
            interval = check_insight_events_query_interval(_interval)

        _unit = d.pop("unit", UNSET)
        unit: InsightEventsQueryUnit | Unset
        if isinstance(_unit, Unset):
            unit = UNSET
        else:
            unit = check_insight_events_query_unit(_unit)

        by = d.pop("by", UNSET)

        _measure = d.pop("measure", UNSET)
        measure: InsightEventsQueryMeasure | Unset
        if isinstance(_measure, Unset):
            measure = UNSET
        else:
            measure = check_insight_events_query_measure(_measure)

        insight_events_query = cls(
            event=event,
            where=where,
            interval=interval,
            unit=unit,
            by=by,
            measure=measure,
        )

        return insight_events_query
