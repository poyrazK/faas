from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_ai_trace import OutboundAITrace
    from ..models.outbound_ai_trace_call import OutboundAITraceCall


T = TypeVar("T", bound="OutboundAITraceResponse")


@_attrs_define
class OutboundAITraceResponse:
    """One AI trace with its logged calls, oldest first."""

    trace: OutboundAITrace
    """One AI trace's totals (ADR 20261020202035). app_id and end_user are its first call's; repeated_tool_calls is
    its current streak of calls that requested the same tools with the same arguments."""
    calls: list[OutboundAITraceCall]
    calls_truncated: bool
    """Whether the trace logged more calls than were returned."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        trace = self.trace.to_dict()

        calls = []
        for calls_item_data in self.calls:
            calls_item = calls_item_data.to_dict()
            calls.append(calls_item)

        calls_truncated = self.calls_truncated

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "trace": trace,
                "calls": calls,
                "calls_truncated": calls_truncated,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_trace import OutboundAITrace
        from ..models.outbound_ai_trace_call import OutboundAITraceCall

        d = dict(src_dict)
        trace = OutboundAITrace.from_dict(d.pop("trace"))

        calls = []
        _calls = d.pop("calls")
        for calls_item_data in _calls:
            calls_item = OutboundAITraceCall.from_dict(calls_item_data)

            calls.append(calls_item)

        calls_truncated = d.pop("calls_truncated")

        outbound_ai_trace_response = cls(
            trace=trace,
            calls=calls,
            calls_truncated=calls_truncated,
        )

        outbound_ai_trace_response.additional_properties = d
        return outbound_ai_trace_response

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
