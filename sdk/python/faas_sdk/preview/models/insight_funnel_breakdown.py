from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.insight_funnel_step import InsightFunnelStep


T = TypeVar("T", bound="InsightFunnelBreakdown")


@_attrs_define
class InsightFunnelBreakdown:
    """A funnel for one breakdown value."""

    value: str
    steps: list[InsightFunnelStep]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        value = self.value

        steps = []
        for steps_item_data in self.steps:
            steps_item = steps_item_data.to_dict()
            steps.append(steps_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "value": value,
                "steps": steps,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.insight_funnel_step import InsightFunnelStep

        d = dict(src_dict)
        value = d.pop("value")

        steps = []
        _steps = d.pop("steps")
        for steps_item_data in _steps:
            steps_item = InsightFunnelStep.from_dict(steps_item_data)

            steps.append(steps_item)

        insight_funnel_breakdown = cls(
            value=value,
            steps=steps,
        )

        insight_funnel_breakdown.additional_properties = d
        return insight_funnel_breakdown

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
