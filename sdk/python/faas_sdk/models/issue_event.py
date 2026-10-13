from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.issue_event_frame_order import IssueEventFrameOrder, check_issue_event_frame_order
from ..models.issue_event_grouping_strategy import IssueEventGroupingStrategy, check_issue_event_grouping_strategy
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.issue_cause import IssueCause
    from ..models.issue_frame import IssueFrame


T = TypeVar("T", bound="IssueEvent")


@_attrs_define
class IssueEvent:
    """Structured exception evidence; credential-derived account and deployment identity cannot be overridden."""

    event_id: UUID
    occurred_at: datetime.datetime
    exception_type: str
    message: str
    causes: list[IssueCause] | Unset = UNSET
    causes_incomplete: bool | Unset = UNSET
    """Incomplete or ambiguous chain; version 3 uses outer evidence instead of a partial cause."""
    stack_trace: str | Unset = UNSET
    frames: list[IssueFrame] | Unset = UNSET
    fingerprint_override: str | Unset = UNSET
    grouping_strategy: IssueEventGroupingStrategy | Unset = UNSET
    """Defaults to unchanged version 1. failure-site-v1 selects version 2 and creates separate groups without
    migrating existing history. cause-chain-v1 selects version 3 using the deepest complete reported cause."""
    frame_order: IssueEventFrameOrder | Unset = UNSET
    """For failure-site-v1, structured frames default to newest-first. Parsed Node/Python stacks infer order when
    omitted."""
    error_code: str | Unset = UNSET
    """Stable application error code, bounded to 256 UTF-8 bytes and sanitized. In failure-site-v1 it replaces the
    normalized message discriminator; ignored by legacy grouping."""
    trace_id: str | Unset = UNSET
    span_id: str | Unset = UNSET
    request_id: str | Unset = UNSET
    """Bounded opaque gateway request ID, sanitized before persistence."""
    invocation_id: UUID | Unset = UNSET
    route: str | Unset = UNSET
    http_status: int | Unset = UNSET
    source_kind: str | Unset = UNSET
    redactions: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        occurred_at = self.occurred_at.isoformat()

        exception_type = self.exception_type

        message = self.message

        causes: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.causes, Unset):
            causes = []
            for causes_item_data in self.causes:
                causes_item = causes_item_data.to_dict()
                causes.append(causes_item)

        causes_incomplete = self.causes_incomplete

        stack_trace = self.stack_trace

        frames: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.frames, Unset):
            frames = []
            for frames_item_data in self.frames:
                frames_item = frames_item_data.to_dict()
                frames.append(frames_item)

        fingerprint_override = self.fingerprint_override

        grouping_strategy: str | Unset = UNSET
        if not isinstance(self.grouping_strategy, Unset):
            grouping_strategy = self.grouping_strategy

        frame_order: str | Unset = UNSET
        if not isinstance(self.frame_order, Unset):
            frame_order = self.frame_order

        error_code = self.error_code

        trace_id = self.trace_id

        span_id = self.span_id

        request_id = self.request_id

        invocation_id: str | Unset = UNSET
        if not isinstance(self.invocation_id, Unset):
            invocation_id = str(self.invocation_id)

        route = self.route

        http_status = self.http_status

        source_kind = self.source_kind

        redactions: list[str] | Unset = UNSET
        if not isinstance(self.redactions, Unset):
            redactions = self.redactions

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "occurred_at": occurred_at,
                "exception_type": exception_type,
                "message": message,
            }
        )
        if causes is not UNSET:
            field_dict["causes"] = causes
        if causes_incomplete is not UNSET:
            field_dict["causes_incomplete"] = causes_incomplete
        if stack_trace is not UNSET:
            field_dict["stack_trace"] = stack_trace
        if frames is not UNSET:
            field_dict["frames"] = frames
        if fingerprint_override is not UNSET:
            field_dict["fingerprint_override"] = fingerprint_override
        if grouping_strategy is not UNSET:
            field_dict["grouping_strategy"] = grouping_strategy
        if frame_order is not UNSET:
            field_dict["frame_order"] = frame_order
        if error_code is not UNSET:
            field_dict["error_code"] = error_code
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id
        if span_id is not UNSET:
            field_dict["span_id"] = span_id
        if request_id is not UNSET:
            field_dict["request_id"] = request_id
        if invocation_id is not UNSET:
            field_dict["invocation_id"] = invocation_id
        if route is not UNSET:
            field_dict["route"] = route
        if http_status is not UNSET:
            field_dict["http_status"] = http_status
        if source_kind is not UNSET:
            field_dict["source_kind"] = source_kind
        if redactions is not UNSET:
            field_dict["redactions"] = redactions

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_cause import IssueCause
        from ..models.issue_frame import IssueFrame

        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        exception_type = d.pop("exception_type")

        message = d.pop("message")

        _causes = d.pop("causes", UNSET)
        causes: list[IssueCause] | Unset = UNSET
        if _causes is not UNSET:
            causes = []
            for causes_item_data in _causes:
                causes_item = IssueCause.from_dict(causes_item_data)

                causes.append(causes_item)

        causes_incomplete = d.pop("causes_incomplete", UNSET)

        stack_trace = d.pop("stack_trace", UNSET)

        _frames = d.pop("frames", UNSET)
        frames: list[IssueFrame] | Unset = UNSET
        if _frames is not UNSET:
            frames = []
            for frames_item_data in _frames:
                frames_item = IssueFrame.from_dict(frames_item_data)

                frames.append(frames_item)

        fingerprint_override = d.pop("fingerprint_override", UNSET)

        _grouping_strategy = d.pop("grouping_strategy", UNSET)
        grouping_strategy: IssueEventGroupingStrategy | Unset
        if isinstance(_grouping_strategy, Unset):
            grouping_strategy = UNSET
        else:
            grouping_strategy = check_issue_event_grouping_strategy(_grouping_strategy)

        _frame_order = d.pop("frame_order", UNSET)
        frame_order: IssueEventFrameOrder | Unset
        if isinstance(_frame_order, Unset):
            frame_order = UNSET
        else:
            frame_order = check_issue_event_frame_order(_frame_order)

        error_code = d.pop("error_code", UNSET)

        trace_id = d.pop("trace_id", UNSET)

        span_id = d.pop("span_id", UNSET)

        request_id = d.pop("request_id", UNSET)

        _invocation_id = d.pop("invocation_id", UNSET)
        invocation_id: UUID | Unset
        if isinstance(_invocation_id, Unset):
            invocation_id = UNSET
        else:
            invocation_id = UUID(_invocation_id)

        route = d.pop("route", UNSET)

        http_status = d.pop("http_status", UNSET)

        source_kind = d.pop("source_kind", UNSET)

        redactions = cast(list[str], d.pop("redactions", UNSET))

        issue_event = cls(
            event_id=event_id,
            occurred_at=occurred_at,
            exception_type=exception_type,
            message=message,
            causes=causes,
            causes_incomplete=causes_incomplete,
            stack_trace=stack_trace,
            frames=frames,
            fingerprint_override=fingerprint_override,
            grouping_strategy=grouping_strategy,
            frame_order=frame_order,
            error_code=error_code,
            trace_id=trace_id,
            span_id=span_id,
            request_id=request_id,
            invocation_id=invocation_id,
            route=route,
            http_status=http_status,
            source_kind=source_kind,
            redactions=redactions,
        )

        issue_event.additional_properties = d
        return issue_event

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
