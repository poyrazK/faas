from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.insight_step_request import InsightStepRequest


T = TypeVar("T", bound="FlagExperimentFunnel")


@_attrs_define
class FlagExperimentFunnel:
    """A funnel metric: a customer converts when it completes the steps in order within the window after its first
    exposure.

    """

    steps: list[InsightStepRequest]
    window: str
    """Go duration from first exposure, 1h to 720h."""

    def to_dict(self) -> dict[str, Any]:
        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        window = self.window

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "steps": steps,
                "window": window,
            }
        )

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

        window = d.pop("window")

        flag_experiment_funnel = cls(
            steps=steps,
            window=window,
        )

        return flag_experiment_funnel
