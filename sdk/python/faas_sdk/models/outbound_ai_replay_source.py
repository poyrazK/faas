from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OutboundAIReplaySource")


@_attrs_define
class OutboundAIReplaySource:
    """The captured call a replay resends."""

    model: str
    status: int
    input_tokens: int
    output_tokens: int
    cost_millicents: int
    duration_ms: int
    ttfb_ms: int | None
    output_text: None | str
    """Text read from the captured response."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        model = self.model

        status = self.status

        input_tokens = self.input_tokens

        output_tokens = self.output_tokens

        cost_millicents = self.cost_millicents

        duration_ms = self.duration_ms

        ttfb_ms: int | None
        ttfb_ms = self.ttfb_ms

        output_text: None | str
        output_text = self.output_text

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "model": model,
                "status": status,
                "input_tokens": input_tokens,
                "output_tokens": output_tokens,
                "cost_millicents": cost_millicents,
                "duration_ms": duration_ms,
                "ttfb_ms": ttfb_ms,
                "output_text": output_text,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        model = d.pop("model")

        status = d.pop("status")

        input_tokens = d.pop("input_tokens")

        output_tokens = d.pop("output_tokens")

        cost_millicents = d.pop("cost_millicents")

        duration_ms = d.pop("duration_ms")

        def _parse_ttfb_ms(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        ttfb_ms = _parse_ttfb_ms(d.pop("ttfb_ms"))

        def _parse_output_text(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        output_text = _parse_output_text(d.pop("output_text"))

        outbound_ai_replay_source = cls(
            model=model,
            status=status,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            cost_millicents=cost_millicents,
            duration_ms=duration_ms,
            ttfb_ms=ttfb_ms,
            output_text=output_text,
        )

        outbound_ai_replay_source.additional_properties = d
        return outbound_ai_replay_source

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
