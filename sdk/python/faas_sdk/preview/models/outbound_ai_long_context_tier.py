from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OutboundAILongContextTier")


@_attrs_define
class OutboundAILongContextTier:
    """A higher price for a long prompt. When a call's input tokens (cached reads and cache writes included) exceed
    above_input_tokens, its input-side prices are multiplied by input_percent/100 and its output prices by
    output_percent/100.

    """

    above_input_tokens: int
    input_percent: int
    output_percent: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        above_input_tokens = self.above_input_tokens

        input_percent = self.input_percent

        output_percent = self.output_percent

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "above_input_tokens": above_input_tokens,
                "input_percent": input_percent,
                "output_percent": output_percent,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        above_input_tokens = d.pop("above_input_tokens")

        input_percent = d.pop("input_percent")

        output_percent = d.pop("output_percent")

        outbound_ai_long_context_tier = cls(
            above_input_tokens=above_input_tokens,
            input_percent=input_percent,
            output_percent=output_percent,
        )

        outbound_ai_long_context_tier.additional_properties = d
        return outbound_ai_long_context_tier

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
