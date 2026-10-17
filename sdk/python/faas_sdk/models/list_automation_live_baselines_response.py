from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.automation_live_baseline_health import AutomationLiveBaselineHealth


T = TypeVar("T", bound="ListAutomationLiveBaselinesResponse")


@_attrs_define
class ListAutomationLiveBaselinesResponse:
    """Bounded sorted catalog of active named baseline heads and their current evidence observations."""

    baselines: list[AutomationLiveBaselineHealth]
    observed_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        baselines = []
        for baselines_item_data in self.baselines:
            baselines_item = baselines_item_data.to_dict()
            baselines.append(baselines_item)

        observed_at = self.observed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "baselines": baselines,
                "observed_at": observed_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_live_baseline_health import AutomationLiveBaselineHealth

        d = dict(src_dict)
        baselines = []
        _baselines = d.pop("baselines")
        for baselines_item_data in _baselines:
            baselines_item = AutomationLiveBaselineHealth.from_dict(baselines_item_data)

            baselines.append(baselines_item)

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        list_automation_live_baselines_response = cls(
            baselines=baselines,
            observed_at=observed_at,
        )

        return list_automation_live_baselines_response
