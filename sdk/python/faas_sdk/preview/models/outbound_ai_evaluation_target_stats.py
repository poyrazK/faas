from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OutboundAIEvaluationTargetStats")


@_attrs_define
class OutboundAIEvaluationTargetStats:
    """One side's total cost over the sample and mean time to first byte over the calls that reported one."""

    cost_millicents: int
    mean_ttfb_ms: int | None
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        cost_millicents = self.cost_millicents

        mean_ttfb_ms: int | None
        mean_ttfb_ms = self.mean_ttfb_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "cost_millicents": cost_millicents,
                "mean_ttfb_ms": mean_ttfb_ms,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        cost_millicents = d.pop("cost_millicents")

        def _parse_mean_ttfb_ms(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        mean_ttfb_ms = _parse_mean_ttfb_ms(d.pop("mean_ttfb_ms"))

        outbound_ai_evaluation_target_stats = cls(
            cost_millicents=cost_millicents,
            mean_ttfb_ms=mean_ttfb_ms,
        )

        outbound_ai_evaluation_target_stats.additional_properties = d
        return outbound_ai_evaluation_target_stats

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
