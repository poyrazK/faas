from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OutboundAITrace")


@_attrs_define
class OutboundAITrace:
    """One AI trace's totals (ADR 20261020202035). app_id and end_user are its first call's; repeated_tool_calls is its
    current streak of calls that requested the same tools with the same arguments.

    """

    trace_id: str
    app_id: None | UUID
    end_user: None | str
    first_at: datetime.datetime
    last_at: datetime.datetime
    calls: int
    error_calls: int
    usage_unknown_calls: int
    input_tokens: int
    output_tokens: int
    cost_millicents: int
    tool_calls: int
    """Tool calls the trace's responses requested."""
    repeated_tool_calls: int
    models: list[str]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        trace_id = self.trace_id

        app_id: None | str
        if isinstance(self.app_id, UUID):
            app_id = str(self.app_id)
        else:
            app_id = self.app_id

        end_user: None | str
        end_user = self.end_user

        first_at = self.first_at.isoformat()

        last_at = self.last_at.isoformat()

        calls = self.calls

        error_calls = self.error_calls

        usage_unknown_calls = self.usage_unknown_calls

        input_tokens = self.input_tokens

        output_tokens = self.output_tokens

        cost_millicents = self.cost_millicents

        tool_calls = self.tool_calls

        repeated_tool_calls = self.repeated_tool_calls

        models = self.models

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "trace_id": trace_id,
                "app_id": app_id,
                "end_user": end_user,
                "first_at": first_at,
                "last_at": last_at,
                "calls": calls,
                "error_calls": error_calls,
                "usage_unknown_calls": usage_unknown_calls,
                "input_tokens": input_tokens,
                "output_tokens": output_tokens,
                "cost_millicents": cost_millicents,
                "tool_calls": tool_calls,
                "repeated_tool_calls": repeated_tool_calls,
                "models": models,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        trace_id = d.pop("trace_id")

        def _parse_app_id(data: object) -> None | UUID:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                app_id_type_0 = UUID(data)

                return app_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | UUID, data)

        app_id = _parse_app_id(d.pop("app_id"))

        def _parse_end_user(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        end_user = _parse_end_user(d.pop("end_user"))

        first_at = datetime.datetime.fromisoformat(d.pop("first_at"))

        last_at = datetime.datetime.fromisoformat(d.pop("last_at"))

        calls = d.pop("calls")

        error_calls = d.pop("error_calls")

        usage_unknown_calls = d.pop("usage_unknown_calls")

        input_tokens = d.pop("input_tokens")

        output_tokens = d.pop("output_tokens")

        cost_millicents = d.pop("cost_millicents")

        tool_calls = d.pop("tool_calls")

        repeated_tool_calls = d.pop("repeated_tool_calls")

        models = cast(list[str], d.pop("models"))

        outbound_ai_trace = cls(
            trace_id=trace_id,
            app_id=app_id,
            end_user=end_user,
            first_at=first_at,
            last_at=last_at,
            calls=calls,
            error_calls=error_calls,
            usage_unknown_calls=usage_unknown_calls,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            cost_millicents=cost_millicents,
            tool_calls=tool_calls,
            repeated_tool_calls=repeated_tool_calls,
            models=models,
        )

        outbound_ai_trace.additional_properties = d
        return outbound_ai_trace

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
