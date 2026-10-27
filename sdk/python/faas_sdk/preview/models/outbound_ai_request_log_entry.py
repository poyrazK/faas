from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_ai_request_log_entry_feedback import (
    OutboundAIRequestLogEntryFeedback,
    check_outbound_ai_request_log_entry_feedback,
)
from ..models.outbound_ai_request_log_entry_outcome import (
    OutboundAIRequestLogEntryOutcome,
    check_outbound_ai_request_log_entry_outcome,
)
from ..models.outbound_ai_request_log_entry_quality_flags_item import (
    OutboundAIRequestLogEntryQualityFlagsItem,
    check_outbound_ai_request_log_entry_quality_flags_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OutboundAIRequestLogEntry")


@_attrs_define
class OutboundAIRequestLogEntry:
    """One settled AI call's metadata. Its bodies are kept only when the integration captures content."""

    id: UUID
    created_at: datetime.datetime
    caller: str
    """App UUID, or "runs"."""
    route_id: None | UUID
    model: str
    status: int
    outcome: OutboundAIRequestLogEntryOutcome
    input_tokens: int
    cached_input_tokens: int
    cache_write_tokens: int
    output_tokens: int
    cost_millicents: int
    duration_ms: int
    ttfb_ms: int | None
    attempts: int
    end_user: None | str
    """The call's X-Gregale-AI-User value."""
    content_captured: bool
    """Whether the call's bodies can be read from its content endpoint."""
    tags: list[str]
    """The call's X-Gregale-AI-Tags as key=value, sorted by key."""
    quality_flags: list[OutboundAIRequestLogEntryQualityFlagsItem] | Unset = UNSET
    """What went wrong with the answer."""
    feedback: OutboundAIRequestLogEntryFeedback | Unset = UNSET
    """The call's rating."""
    feedback_label: str | Unset = UNSET
    """The rating's label."""
    evaluation_score: int | Unset = UNSET
    """The online evaluation's score."""
    trace_id: str | Unset = UNSET
    """The call's AI trace, when the integration records traces."""
    tool_calls: list[str] | Unset = UNSET
    """The names of the tools the call's response requested, in order."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        created_at = self.created_at.isoformat()

        caller = self.caller

        route_id: None | str
        if isinstance(self.route_id, UUID):
            route_id = str(self.route_id)
        else:
            route_id = self.route_id

        model = self.model

        status = self.status

        outcome: str = self.outcome

        input_tokens = self.input_tokens

        cached_input_tokens = self.cached_input_tokens

        cache_write_tokens = self.cache_write_tokens

        output_tokens = self.output_tokens

        cost_millicents = self.cost_millicents

        duration_ms = self.duration_ms

        ttfb_ms: int | None
        ttfb_ms = self.ttfb_ms

        attempts = self.attempts

        end_user: None | str
        end_user = self.end_user

        content_captured = self.content_captured

        tags = self.tags

        quality_flags: list[str] | Unset = UNSET
        if not isinstance(self.quality_flags, Unset):
            quality_flags = []
            for quality_flags_item_data in self.quality_flags:
                quality_flags_item: str = quality_flags_item_data
                quality_flags.append(quality_flags_item)

        feedback: str | Unset = UNSET
        if not isinstance(self.feedback, Unset):
            feedback = self.feedback

        feedback_label = self.feedback_label

        evaluation_score = self.evaluation_score

        trace_id = self.trace_id

        tool_calls: list[str] | Unset = UNSET
        if not isinstance(self.tool_calls, Unset):
            tool_calls = self.tool_calls

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "created_at": created_at,
                "caller": caller,
                "route_id": route_id,
                "model": model,
                "status": status,
                "outcome": outcome,
                "input_tokens": input_tokens,
                "cached_input_tokens": cached_input_tokens,
                "cache_write_tokens": cache_write_tokens,
                "output_tokens": output_tokens,
                "cost_millicents": cost_millicents,
                "duration_ms": duration_ms,
                "ttfb_ms": ttfb_ms,
                "attempts": attempts,
                "end_user": end_user,
                "content_captured": content_captured,
                "tags": tags,
            }
        )
        if quality_flags is not UNSET:
            field_dict["quality_flags"] = quality_flags
        if feedback is not UNSET:
            field_dict["feedback"] = feedback
        if feedback_label is not UNSET:
            field_dict["feedback_label"] = feedback_label
        if evaluation_score is not UNSET:
            field_dict["evaluation_score"] = evaluation_score
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id
        if tool_calls is not UNSET:
            field_dict["tool_calls"] = tool_calls

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        caller = d.pop("caller")

        def _parse_route_id(data: object) -> None | UUID:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                route_id_type_0 = UUID(data)

                return route_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | UUID, data)

        route_id = _parse_route_id(d.pop("route_id"))

        model = d.pop("model")

        status = d.pop("status")

        outcome = check_outbound_ai_request_log_entry_outcome(d.pop("outcome"))

        input_tokens = d.pop("input_tokens")

        cached_input_tokens = d.pop("cached_input_tokens")

        cache_write_tokens = d.pop("cache_write_tokens")

        output_tokens = d.pop("output_tokens")

        cost_millicents = d.pop("cost_millicents")

        duration_ms = d.pop("duration_ms")

        def _parse_ttfb_ms(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        ttfb_ms = _parse_ttfb_ms(d.pop("ttfb_ms"))

        attempts = d.pop("attempts")

        def _parse_end_user(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        end_user = _parse_end_user(d.pop("end_user"))

        content_captured = d.pop("content_captured")

        tags = cast(list[str], d.pop("tags"))

        _quality_flags = d.pop("quality_flags", UNSET)
        quality_flags: list[OutboundAIRequestLogEntryQualityFlagsItem] | Unset = UNSET
        if _quality_flags is not UNSET:
            quality_flags = []
            for quality_flags_item_data in _quality_flags:
                quality_flags_item = check_outbound_ai_request_log_entry_quality_flags_item(quality_flags_item_data)

                quality_flags.append(quality_flags_item)

        _feedback = d.pop("feedback", UNSET)
        feedback: OutboundAIRequestLogEntryFeedback | Unset
        if isinstance(_feedback, Unset):
            feedback = UNSET
        else:
            feedback = check_outbound_ai_request_log_entry_feedback(_feedback)

        feedback_label = d.pop("feedback_label", UNSET)

        evaluation_score = d.pop("evaluation_score", UNSET)

        trace_id = d.pop("trace_id", UNSET)

        tool_calls = cast(list[str], d.pop("tool_calls", UNSET))

        outbound_ai_request_log_entry = cls(
            id=id,
            created_at=created_at,
            caller=caller,
            route_id=route_id,
            model=model,
            status=status,
            outcome=outcome,
            input_tokens=input_tokens,
            cached_input_tokens=cached_input_tokens,
            cache_write_tokens=cache_write_tokens,
            output_tokens=output_tokens,
            cost_millicents=cost_millicents,
            duration_ms=duration_ms,
            ttfb_ms=ttfb_ms,
            attempts=attempts,
            end_user=end_user,
            content_captured=content_captured,
            tags=tags,
            quality_flags=quality_flags,
            feedback=feedback,
            feedback_label=feedback_label,
            evaluation_score=evaluation_score,
            trace_id=trace_id,
            tool_calls=tool_calls,
        )

        outbound_ai_request_log_entry.additional_properties = d
        return outbound_ai_request_log_entry

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
