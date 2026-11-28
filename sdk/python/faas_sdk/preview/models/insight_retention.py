from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.insight_retention_period import InsightRetentionPeriod, check_insight_retention_period
from ..models.insight_retention_unit import InsightRetentionUnit, check_insight_retention_unit
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.insight_retention_row import InsightRetentionRow


T = TypeVar("T", bound="InsightRetention")


@_attrs_define
class InsightRetention:
    """A retention result."""

    unit: InsightRetentionUnit
    period: InsightRetentionPeriod
    periods: int
    from_: datetime.datetime
    until: datetime.datetime
    overall: InsightRetentionRow
    """A retention cohort, breakdown value or the overall row."""
    cohorts: list[InsightRetentionRow]
    truncated: bool
    by: str | Unset = UNSET
    breakdowns: list[InsightRetentionRow] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        unit: str = self.unit

        period: str = self.period

        periods = self.periods

        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        overall = self.overall.to_dict()

        cohorts = []
        for cohorts_item_data in self.cohorts:
            cohorts_item = cohorts_item_data.to_dict()
            cohorts.append(cohorts_item)

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
                "period": period,
                "periods": periods,
                "from": from_,
                "until": until,
                "overall": overall,
                "cohorts": cohorts,
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
        from ..models.insight_retention_row import InsightRetentionRow

        d = dict(src_dict)
        unit = check_insight_retention_unit(d.pop("unit"))

        period = check_insight_retention_period(d.pop("period"))

        periods = d.pop("periods")

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        overall = InsightRetentionRow.from_dict(d.pop("overall"))

        cohorts = []
        _cohorts = d.pop("cohorts")
        for cohorts_item_data in _cohorts:
            cohorts_item = InsightRetentionRow.from_dict(cohorts_item_data)

            cohorts.append(cohorts_item)

        truncated = d.pop("truncated")

        by = d.pop("by", UNSET)

        _breakdowns = d.pop("breakdowns", UNSET)
        breakdowns: list[InsightRetentionRow] | Unset = UNSET
        if _breakdowns is not UNSET:
            breakdowns = []
            for breakdowns_item_data in _breakdowns:
                breakdowns_item = InsightRetentionRow.from_dict(breakdowns_item_data)

                breakdowns.append(breakdowns_item)

        insight_retention = cls(
            unit=unit,
            period=period,
            periods=periods,
            from_=from_,
            until=until,
            overall=overall,
            cohorts=cohorts,
            truncated=truncated,
            by=by,
            breakdowns=breakdowns,
        )

        insight_retention.additional_properties = d
        return insight_retention

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
