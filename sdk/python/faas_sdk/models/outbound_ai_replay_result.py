from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_ai_content_body import OutboundAIContentBody


T = TypeVar("T", bound="OutboundAIReplayResult")


@_attrs_define
class OutboundAIReplayResult:
    """One target's replayed call. status is null without a response and outcome null when the call was refused before it
    reached the provider.

    """

    integration_id: UUID
    model: None | str
    status: int | None
    outcome: None | str
    """known, unknown or error; null when the call did not settle."""
    error_code: None | str
    """Problem code of a refused or failed call."""
    input_tokens: int
    output_tokens: int
    cost_millicents: int
    duration_ms: int
    ttfb_ms: int | None
    output_text: str
    """Up to 4 KiB of the response text, redacted."""
    response: OutboundAIContentBody
    """The start of one captured body."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        integration_id = str(self.integration_id)

        model: None | str
        model = self.model

        status: int | None
        status = self.status

        outcome: None | str
        outcome = self.outcome

        error_code: None | str
        error_code = self.error_code

        input_tokens = self.input_tokens

        output_tokens = self.output_tokens

        cost_millicents = self.cost_millicents

        duration_ms = self.duration_ms

        ttfb_ms: int | None
        ttfb_ms = self.ttfb_ms

        output_text = self.output_text

        response = self.response.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "integration_id": integration_id,
                "model": model,
                "status": status,
                "outcome": outcome,
                "error_code": error_code,
                "input_tokens": input_tokens,
                "output_tokens": output_tokens,
                "cost_millicents": cost_millicents,
                "duration_ms": duration_ms,
                "ttfb_ms": ttfb_ms,
                "output_text": output_text,
                "response": response,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_content_body import OutboundAIContentBody

        d = dict(src_dict)
        integration_id = UUID(d.pop("integration_id"))

        def _parse_model(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        model = _parse_model(d.pop("model"))

        def _parse_status(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        status = _parse_status(d.pop("status"))

        def _parse_outcome(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        outcome = _parse_outcome(d.pop("outcome"))

        def _parse_error_code(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        error_code = _parse_error_code(d.pop("error_code"))

        input_tokens = d.pop("input_tokens")

        output_tokens = d.pop("output_tokens")

        cost_millicents = d.pop("cost_millicents")

        duration_ms = d.pop("duration_ms")

        def _parse_ttfb_ms(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        ttfb_ms = _parse_ttfb_ms(d.pop("ttfb_ms"))

        output_text = d.pop("output_text")

        response = OutboundAIContentBody.from_dict(d.pop("response"))

        outbound_ai_replay_result = cls(
            integration_id=integration_id,
            model=model,
            status=status,
            outcome=outcome,
            error_code=error_code,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            cost_millicents=cost_millicents,
            duration_ms=duration_ms,
            ttfb_ms=ttfb_ms,
            output_text=output_text,
            response=response,
        )

        outbound_ai_replay_result.additional_properties = d
        return outbound_ai_replay_result

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
