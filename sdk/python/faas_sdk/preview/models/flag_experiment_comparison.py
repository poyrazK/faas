from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="FlagExperimentComparison")


@_attrs_define
class FlagExperimentComparison:
    """A variant's comparison with control without CUPED."""

    lift: float
    relative_lift: float
    low: float
    high: float
    p_value: float
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        lift = self.lift

        relative_lift = self.relative_lift

        low = self.low

        high = self.high

        p_value = self.p_value

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "lift": lift,
                "relative_lift": relative_lift,
                "low": low,
                "high": high,
                "p_value": p_value,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        lift = d.pop("lift")

        relative_lift = d.pop("relative_lift")

        low = d.pop("low")

        high = d.pop("high")

        p_value = d.pop("p_value")

        flag_experiment_comparison = cls(
            lift=lift,
            relative_lift=relative_lift,
            low=low,
            high=high,
            p_value=p_value,
        )

        flag_experiment_comparison.additional_properties = d
        return flag_experiment_comparison

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
