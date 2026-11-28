from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_pipeline_quality_result_state import (
    DataPipelineQualityResultState,
    check_data_pipeline_quality_result_state,
)

T = TypeVar("T", bound="DataPipelineQualityResult")


@_attrs_define
class DataPipelineQualityResult:
    """One curated rule outcome. A lower-bound count reflects limited coverage; zero violations with incomplete coverage is
    inconclusive rather than passed.

    """

    name: str
    state: DataPipelineQualityResultState
    violations: int
    count_is_lower_bound: bool

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        state: str = self.state

        violations = self.violations

        count_is_lower_bound = self.count_is_lower_bound

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "state": state,
                "violations": violations,
                "count_is_lower_bound": count_is_lower_bound,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        state = check_data_pipeline_quality_result_state(d.pop("state"))

        violations = d.pop("violations")

        count_is_lower_bound = d.pop("count_is_lower_bound")

        data_pipeline_quality_result = cls(
            name=name,
            state=state,
            violations=violations,
            count_is_lower_bound=count_is_lower_bound,
        )

        return data_pipeline_quality_result
