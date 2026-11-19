from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.insight_funnel_request_unit import InsightFunnelRequestUnit, check_insight_funnel_request_unit
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.insight_step_request import InsightStepRequest


T = TypeVar("T", bound="InsightFunnelRequest")


@_attrs_define
class InsightFunnelRequest:
    """A funnel query."""

    steps: list[InsightStepRequest]
    from_: datetime.datetime | Unset = UNSET
    until: datetime.datetime | Unset = UNSET
    window: str | Unset = UNSET
    """Go duration from the first step to the last, 1h to 720h; default 168h."""
    unit: InsightFunnelRequestUnit | Unset = "customer"
    by: str | Unset = UNSET
    """flag:KEY or property:KEY."""

    def to_dict(self) -> dict[str, Any]:
        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        from_: str | Unset = UNSET
        if not isinstance(self.from_, Unset):
            from_ = self.from_.isoformat()

        until: str | Unset = UNSET
        if not isinstance(self.until, Unset):
            until = self.until.isoformat()

        window = self.window

        unit: str | Unset = UNSET
        if not isinstance(self.unit, Unset):
            unit = self.unit

        by = self.by

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "steps": steps,
            }
        )
        if from_ is not UNSET:
            field_dict["from"] = from_
        if until is not UNSET:
            field_dict["until"] = until
        if window is not UNSET:
            field_dict["window"] = window
        if unit is not UNSET:
            field_dict["unit"] = unit
        if by is not UNSET:
            field_dict["by"] = by

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.insight_step_request import InsightStepRequest

        d = dict(src_dict)
        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = InsightStepRequest.from_dict(steps_item_data)

            steps.append(steps_item)

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

        window = d.pop("window", UNSET)

        _unit = d.pop("unit", UNSET)
        unit: InsightFunnelRequestUnit | Unset
        if isinstance(_unit, Unset):
            unit = UNSET
        else:
            unit = check_insight_funnel_request_unit(_unit)

        by = d.pop("by", UNSET)

        insight_funnel_request = cls(
            steps=steps,
            from_=from_,
            until=until,
            window=window,
            unit=unit,
            by=by,
        )

        return insight_funnel_request
