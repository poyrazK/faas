from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.insight_retention_request_period import (
    InsightRetentionRequestPeriod,
    check_insight_retention_request_period,
)
from ..models.insight_retention_request_unit import InsightRetentionRequestUnit, check_insight_retention_request_unit
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.insight_step_request import InsightStepRequest


T = TypeVar("T", bound="InsightRetentionRequest")


@_attrs_define
class InsightRetentionRequest:
    """A retention query."""

    start: InsightStepRequest
    """A funnel or retention step: an event and property values its events must carry."""
    return_: InsightStepRequest
    """A funnel or retention step: an event and property values its events must carry."""
    from_: datetime.datetime | Unset = UNSET
    until: datetime.datetime | Unset = UNSET
    period: InsightRetentionRequestPeriod | Unset = "day"
    periods: int | Unset = UNSET
    unit: InsightRetentionRequestUnit | Unset = "customer"
    by: str | Unset = UNSET
    """Split cohorts by flag:KEY or property:KEY."""

    def to_dict(self) -> dict[str, Any]:
        start = self.start.to_dict()

        return_ = self.return_.to_dict()

        from_: str | Unset = UNSET
        if not isinstance(self.from_, Unset):
            from_ = self.from_.isoformat()

        until: str | Unset = UNSET
        if not isinstance(self.until, Unset):
            until = self.until.isoformat()

        period: str | Unset = UNSET
        if not isinstance(self.period, Unset):
            period = self.period

        periods = self.periods

        unit: str | Unset = UNSET
        if not isinstance(self.unit, Unset):
            unit = self.unit

        by = self.by

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "start": start,
                "return": return_,
            }
        )
        if from_ is not UNSET:
            field_dict["from"] = from_
        if until is not UNSET:
            field_dict["until"] = until
        if period is not UNSET:
            field_dict["period"] = period
        if periods is not UNSET:
            field_dict["periods"] = periods
        if unit is not UNSET:
            field_dict["unit"] = unit
        if by is not UNSET:
            field_dict["by"] = by

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.insight_step_request import InsightStepRequest

        d = dict(src_dict)
        start = InsightStepRequest.from_dict(d.pop("start"))

        return_ = InsightStepRequest.from_dict(d.pop("return"))

        _from_ = d.pop("from", UNSET)
        from_: datetime.datetime | Unset
        if isinstance(_from_, Unset):
            from_ = UNSET
        else:
            from_ = datetime.datetime.fromisoformat(_from_)

        _until = d.pop("until", UNSET)
        until: datetime.datetime | Unset
        if isinstance(_until, Unset):
            until = UNSET
        else:
            until = datetime.datetime.fromisoformat(_until)

        _period = d.pop("period", UNSET)
        period: InsightRetentionRequestPeriod | Unset
        if isinstance(_period, Unset):
            period = UNSET
        else:
            period = check_insight_retention_request_period(_period)

        periods = d.pop("periods", UNSET)

        _unit = d.pop("unit", UNSET)
        unit: InsightRetentionRequestUnit | Unset
        if isinstance(_unit, Unset):
            unit = UNSET
        else:
            unit = check_insight_retention_request_unit(_unit)

        by = d.pop("by", UNSET)

        insight_retention_request = cls(
            start=start,
            return_=return_,
            from_=from_,
            until=until,
            period=period,
            periods=periods,
            unit=unit,
            by=by,
        )

        return insight_retention_request
