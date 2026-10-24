from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_ai_trace_call_outcome import OutboundAITraceCallOutcome, check_outbound_ai_trace_call_outcome

T = TypeVar("T", bound="OutboundAITraceCall")


@_attrs_define
class OutboundAITraceCall:
    """One logged call of an AI trace."""

    id: UUID
    created_at: datetime.datetime
    caller: str
    model: str
    status: int
    outcome: OutboundAITraceCallOutcome
    input_tokens: int
    output_tokens: int
    cost_millicents: int
    duration_ms: int
    ttfb_ms: int | None
    tool_calls: list[str]
    """The names of the tools the call's response requested."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        created_at = self.created_at.isoformat()

        caller = self.caller

        model = self.model

        status = self.status

        outcome: str = self.outcome

        input_tokens = self.input_tokens

        output_tokens = self.output_tokens

        cost_millicents = self.cost_millicents

        duration_ms = self.duration_ms

        ttfb_ms: int | None
        ttfb_ms = self.ttfb_ms

        tool_calls = self.tool_calls

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "created_at": created_at,
                "caller": caller,
                "model": model,
                "status": status,
                "outcome": outcome,
                "input_tokens": input_tokens,
                "output_tokens": output_tokens,
                "cost_millicents": cost_millicents,
                "duration_ms": duration_ms,
                "ttfb_ms": ttfb_ms,
                "tool_calls": tool_calls,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        caller = d.pop("caller")

        model = d.pop("model")

        status = d.pop("status")

        outcome = check_outbound_ai_trace_call_outcome(d.pop("outcome"))

        input_tokens = d.pop("input_tokens")

        output_tokens = d.pop("output_tokens")

        cost_millicents = d.pop("cost_millicents")

        duration_ms = d.pop("duration_ms")

        def _parse_ttfb_ms(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        ttfb_ms = _parse_ttfb_ms(d.pop("ttfb_ms"))

        tool_calls = cast(list[str], d.pop("tool_calls"))

        outbound_ai_trace_call = cls(
            id=id,
            created_at=created_at,
            caller=caller,
            model=model,
            status=status,
            outcome=outcome,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            cost_millicents=cost_millicents,
            duration_ms=duration_ms,
            ttfb_ms=ttfb_ms,
            tool_calls=tool_calls,
        )

        outbound_ai_trace_call.additional_properties = d
        return outbound_ai_trace_call

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
