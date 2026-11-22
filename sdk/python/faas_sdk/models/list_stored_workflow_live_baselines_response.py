from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.stored_workflow_live_baseline_health import StoredWorkflowLiveBaselineHealth


T = TypeVar("T", bound="ListStoredWorkflowLiveBaselinesResponse")


@_attrs_define
class ListStoredWorkflowLiveBaselinesResponse:
    """Bounded sorted catalog of active named baseline heads and their current evidence observations."""

    baselines: list[StoredWorkflowLiveBaselineHealth]
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
        from ..models.stored_workflow_live_baseline_health import StoredWorkflowLiveBaselineHealth

        d = dict(src_dict)
        baselines = []
        _baselines = d.pop("baselines")
        for baselines_item_data in _baselines:
            baselines_item = StoredWorkflowLiveBaselineHealth.from_dict(baselines_item_data)

            baselines.append(baselines_item)

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        list_stored_workflow_live_baselines_response = cls(
            baselines=baselines,
            observed_at=observed_at,
        )

        return list_stored_workflow_live_baselines_response
