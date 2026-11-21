from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.insight_funnel_unit import InsightFunnelUnit, check_insight_funnel_unit
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.insight_funnel_breakdown import InsightFunnelBreakdown
    from ..models.insight_funnel_step import InsightFunnelStep


T = TypeVar("T", bound="InsightFunnel")


@_attrs_define
class InsightFunnel:
    """A funnel result."""

    unit: InsightFunnelUnit
    window: str
    from_: datetime.datetime
    until: datetime.datetime
    steps: list[InsightFunnelStep]
    truncated: bool
    """The query read AnalyticsQueryMaxUnits units and left the rest out."""
    by: str | Unset = UNSET
    breakdowns: list[InsightFunnelBreakdown] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        unit: str = self.unit

        window = self.window

        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        truncated = self.truncated

        by = self.by

        breakdowns: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.breakdowns, Unset):
            breakdowns = []
            for breakdowns_item_data in self.breakdowns:
                breakdowns_item = breakdowns_item_data.to_dict()
                breakdowns.append(breakdowns_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "unit": unit,
                "window": window,
                "from": from_,
                "until": until,
                "steps": steps,
                "truncated": truncated,
            }
        )
        if by is not UNSET:
            field_dict["by"] = by
        if breakdowns is not UNSET:
            field_dict["breakdowns"] = breakdowns

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.insight_funnel_breakdown import InsightFunnelBreakdown
        from ..models.insight_funnel_step import InsightFunnelStep

        d = dict(src_dict)
        unit = check_insight_funnel_unit(d.pop("unit"))

        window = d.pop("window")

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = InsightFunnelStep.from_dict(steps_item_data)

            steps.append(steps_item)

        truncated = d.pop("truncated")

        by = d.pop("by", UNSET)

        _breakdowns = d.pop("breakdowns", UNSET)
        breakdowns: list[InsightFunnelBreakdown] | Unset = UNSET
        if _breakdowns is not UNSET:
            breakdowns = []
            for breakdowns_item_data in _breakdowns:
                breakdowns_item = InsightFunnelBreakdown.from_dict(breakdowns_item_data)

                breakdowns.append(breakdowns_item)

        insight_funnel = cls(
            unit=unit,
            window=window,
            from_=from_,
            until=until,
            steps=steps,
            truncated=truncated,
            by=by,
            breakdowns=breakdowns,
        )

        insight_funnel.additional_properties = d
        return insight_funnel

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
