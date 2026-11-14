from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_pipeline_quality_status_state import (
    DataPipelineQualityStatusState,
    check_data_pipeline_quality_status_state,
)

if TYPE_CHECKING:
    from ..models.data_pipeline_quality_result import DataPipelineQualityResult


T = TypeVar("T", bound="DataPipelineQualityStatus")


@_attrs_define
class DataPipelineQualityStatus:
    """Last completed or attempted quality scan for the recorded generation. Unknown and inconclusive are separate from
    passed; failing row values never appear in this observation.

    """

    state: DataPipelineQualityStatusState
    checked_at: datetime.datetime
    next_run_at: datetime.datetime
    generation: int
    results: list[DataPipelineQualityResult]

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        checked_at = self.checked_at.isoformat()

        next_run_at = self.next_run_at.isoformat()

        generation = self.generation

        results = []
        for results_item_data in self.results:
            results_item = results_item_data.to_dict()
            results.append(results_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "state": state,
                "checked_at": checked_at,
                "next_run_at": next_run_at,
                "generation": generation,
                "results": results,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_quality_result import DataPipelineQualityResult

        d = dict(src_dict)
        state = check_data_pipeline_quality_status_state(d.pop("state"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        next_run_at = datetime.datetime.fromisoformat(d.pop("next_run_at"))

        generation = d.pop("generation")

        results = []
        _results = d.pop("results")
        for results_item_data in _results:
            results_item = DataPipelineQualityResult.from_dict(results_item_data)

            results.append(results_item)

        data_pipeline_quality_status = cls(
            state=state,
            checked_at=checked_at,
            next_run_at=next_run_at,
            generation=generation,
            results=results,
        )

        return data_pipeline_quality_status
