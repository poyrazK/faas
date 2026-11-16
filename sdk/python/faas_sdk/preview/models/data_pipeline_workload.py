from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_work_window import DataPipelineWorkWindow


T = TypeVar("T", bound="DataPipelineWorkload")


@_attrs_define
class DataPipelineWorkload:
    """Owner maintenance budgets within hard pilot bounds. Query timeout is zero for the default or 50 through 2000
    milliseconds; batch count is zero for default or one through four. Native COPY bandwidth is outside this policy.

    """

    window: DataPipelineWorkWindow | Unset = UNSET
    """Daily UTC minute window with an inclusive start and exclusive end. Start and end differ; a start after end
    crosses midnight. Omit the window for all-day work."""
    query_timeout_millis: int | Unset = UNSET
    batches_per_tick: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        window: dict[str, Any] | Unset = UNSET
        if not isinstance(self.window, Unset):
            window = self.window.to_dict()

        query_timeout_millis = self.query_timeout_millis

        batches_per_tick = self.batches_per_tick

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if window is not UNSET:
            field_dict["window"] = window
        if query_timeout_millis is not UNSET:
            field_dict["query_timeout_millis"] = query_timeout_millis
        if batches_per_tick is not UNSET:
            field_dict["batches_per_tick"] = batches_per_tick

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_work_window import DataPipelineWorkWindow

        d = dict(src_dict)
        _window = d.pop("window", UNSET)
        window: DataPipelineWorkWindow | Unset
        if isinstance(_window, Unset):
            window = UNSET
        else:
            window = DataPipelineWorkWindow.from_dict(_window)

        query_timeout_millis = d.pop("query_timeout_millis", UNSET)

        batches_per_tick = d.pop("batches_per_tick", UNSET)

        data_pipeline_workload = cls(
            window=window,
            query_timeout_millis=query_timeout_millis,
            batches_per_tick=batches_per_tick,
        )

        return data_pipeline_workload
