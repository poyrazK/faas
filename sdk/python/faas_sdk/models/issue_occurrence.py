from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.issue_event_frame_order import IssueEventFrameOrder, check_issue_event_frame_order
from ..models.issue_event_grouping_strategy import IssueEventGroupingStrategy, check_issue_event_grouping_strategy
from ..models.issue_event_symbolication_strategy import (
    IssueEventSymbolicationStrategy,
    check_issue_event_symbolication_strategy,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.issue_breadcrumb import IssueBreadcrumb
    from ..models.issue_cause import IssueCause
    from ..models.issue_event_tags import IssueEventTags
    from ..models.issue_frame import IssueFrame
    from ..models.issue_grouping_explanation import IssueGroupingExplanation
    from ..models.issue_symbolication_result import IssueSymbolicationResult


T = TypeVar("T", bound="IssueOccurrence")


@_attrs_define
class IssueOccurrence:
    """Retained sanitized occurrence with verified platform attribution and optional debugger correlation."""

    event_id: UUID
    occurred_at: datetime.datetime
    exception_type: str
    message: str
    deployment_id: UUID
    received_at: datetime.datetime
    attribution: str
    breadcrumbs: list[IssueBreadcrumb] | Unset = UNSET
    breadcrumbs_dropped: int | Unset = UNSET
    tags: IssueEventTags | Unset = UNSET
    """Sanitized operational dimensions; unknown keys are discarded. Allowed keys are component, operation,
    feature_flag, feature_variant, dependency, region, job_type, workflow, state, error_class, release_channel,
    runtime."""
    symbolication_strategy: IssueEventSymbolicationStrategy | Unset = UNSET
    """Opt-in server mapping against this credential's deployment artifacts; requires failure-site-v1 or cause-
    chain-v1."""
    symbolication: IssueSymbolicationResult | Unset = UNSET
    """Symbolication outcome and mapped frames for one reported exception in its deployment."""
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
    id: UUID | Unset = UNSET
    original_issue_id: UUID | Unset = UNSET
    """Original fingerprint provenance retained through occurrence-level splits and manual merges."""
    verified_consumer_id: UUID | Unset = UNSET
    verified_platform_tenant_id: UUID | Unset = UNSET
    debug_request_id: UUID | Unset = UNSET
    grouping_explanation: IssueGroupingExplanation | Unset = UNSET
    """Read-only evidence reconstructed from a retained sanitized occurrence using the saved group version. Present
    only when its identity reproduces the saved fingerprint. Unsupported versions or mismatches omit the
    explanation."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        occurred_at = self.occurred_at.isoformat()

        exception_type = self.exception_type

        message = self.message

        deployment_id = str(self.deployment_id)

        received_at = self.received_at.isoformat()

        attribution = self.attribution

        breadcrumbs: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.breadcrumbs, Unset):
            breadcrumbs = []
            for breadcrumbs_item_data in self.breadcrumbs:
                breadcrumbs_item = breadcrumbs_item_data.to_dict()
                breadcrumbs.append(breadcrumbs_item)

        breadcrumbs_dropped = self.breadcrumbs_dropped

        tags: dict[str, Any] | Unset = UNSET
        if not isinstance(self.tags, Unset):
            tags = self.tags.to_dict()

        symbolication_strategy: str | Unset = UNSET
        if not isinstance(self.symbolication_strategy, Unset):
            symbolication_strategy = self.symbolication_strategy

        symbolication: dict[str, Any] | Unset = UNSET
        if not isinstance(self.symbolication, Unset):
            symbolication = self.symbolication.to_dict()

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

        id: str | Unset = UNSET
        if not isinstance(self.id, Unset):
            id = str(self.id)

        original_issue_id: str | Unset = UNSET
        if not isinstance(self.original_issue_id, Unset):
            original_issue_id = str(self.original_issue_id)

        verified_consumer_id: str | Unset = UNSET
        if not isinstance(self.verified_consumer_id, Unset):
            verified_consumer_id = str(self.verified_consumer_id)

        verified_platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.verified_platform_tenant_id, Unset):
            verified_platform_tenant_id = str(self.verified_platform_tenant_id)

        debug_request_id: str | Unset = UNSET
        if not isinstance(self.debug_request_id, Unset):
            debug_request_id = str(self.debug_request_id)

        grouping_explanation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.grouping_explanation, Unset):
            grouping_explanation = self.grouping_explanation.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "occurred_at": occurred_at,
                "exception_type": exception_type,
                "message": message,
                "deployment_id": deployment_id,
                "received_at": received_at,
                "attribution": attribution,
            }
        )
        if breadcrumbs is not UNSET:
            field_dict["breadcrumbs"] = breadcrumbs
        if breadcrumbs_dropped is not UNSET:
            field_dict["breadcrumbs_dropped"] = breadcrumbs_dropped
        if tags is not UNSET:
            field_dict["tags"] = tags
        if symbolication_strategy is not UNSET:
            field_dict["symbolication_strategy"] = symbolication_strategy
        if symbolication is not UNSET:
            field_dict["symbolication"] = symbolication
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
        if id is not UNSET:
            field_dict["id"] = id
        if original_issue_id is not UNSET:
            field_dict["original_issue_id"] = original_issue_id
        if verified_consumer_id is not UNSET:
            field_dict["verified_consumer_id"] = verified_consumer_id
        if verified_platform_tenant_id is not UNSET:
            field_dict["verified_platform_tenant_id"] = verified_platform_tenant_id
        if debug_request_id is not UNSET:
            field_dict["debug_request_id"] = debug_request_id
        if grouping_explanation is not UNSET:
            field_dict["grouping_explanation"] = grouping_explanation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_breadcrumb import IssueBreadcrumb
        from ..models.issue_cause import IssueCause
        from ..models.issue_event_tags import IssueEventTags
        from ..models.issue_frame import IssueFrame
        from ..models.issue_grouping_explanation import IssueGroupingExplanation
        from ..models.issue_symbolication_result import IssueSymbolicationResult

        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        exception_type = d.pop("exception_type")

        message = d.pop("message")

        deployment_id = UUID(d.pop("deployment_id"))

        received_at = datetime.datetime.fromisoformat(d.pop("received_at"))

        attribution = d.pop("attribution")

        _breadcrumbs = d.pop("breadcrumbs", UNSET)
        breadcrumbs: list[IssueBreadcrumb] | Unset = UNSET
        if _breadcrumbs is not UNSET:
            breadcrumbs = []
            for breadcrumbs_item_data in _breadcrumbs:
                breadcrumbs_item = IssueBreadcrumb.from_dict(breadcrumbs_item_data)

                breadcrumbs.append(breadcrumbs_item)

        breadcrumbs_dropped = d.pop("breadcrumbs_dropped", UNSET)

        _tags = d.pop("tags", UNSET)
        tags: IssueEventTags | Unset
        if isinstance(_tags, Unset):
            tags = UNSET
        else:
            tags = IssueEventTags.from_dict(_tags)

        _symbolication_strategy = d.pop("symbolication_strategy", UNSET)
        symbolication_strategy: IssueEventSymbolicationStrategy | Unset
        if isinstance(_symbolication_strategy, Unset):
            symbolication_strategy = UNSET
        else:
            symbolication_strategy = check_issue_event_symbolication_strategy(_symbolication_strategy)

        _symbolication = d.pop("symbolication", UNSET)
        symbolication: IssueSymbolicationResult | Unset
        if isinstance(_symbolication, Unset):
            symbolication = UNSET
        else:
            symbolication = IssueSymbolicationResult.from_dict(_symbolication)

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

        _id = d.pop("id", UNSET)
        id: UUID | Unset
        if isinstance(_id, Unset):
            id = UNSET
        else:
            id = UUID(_id)

        _original_issue_id = d.pop("original_issue_id", UNSET)
        original_issue_id: UUID | Unset
        if isinstance(_original_issue_id, Unset):
            original_issue_id = UNSET
        else:
            original_issue_id = UUID(_original_issue_id)

        _verified_consumer_id = d.pop("verified_consumer_id", UNSET)
        verified_consumer_id: UUID | Unset
        if isinstance(_verified_consumer_id, Unset):
            verified_consumer_id = UNSET
        else:
            verified_consumer_id = UUID(_verified_consumer_id)

        _verified_platform_tenant_id = d.pop("verified_platform_tenant_id", UNSET)
        verified_platform_tenant_id: UUID | Unset
        if isinstance(_verified_platform_tenant_id, Unset):
            verified_platform_tenant_id = UNSET
        else:
            verified_platform_tenant_id = UUID(_verified_platform_tenant_id)

        _debug_request_id = d.pop("debug_request_id", UNSET)
        debug_request_id: UUID | Unset
        if isinstance(_debug_request_id, Unset):
            debug_request_id = UNSET
        else:
            debug_request_id = UUID(_debug_request_id)

        _grouping_explanation = d.pop("grouping_explanation", UNSET)
        grouping_explanation: IssueGroupingExplanation | Unset
        if isinstance(_grouping_explanation, Unset):
            grouping_explanation = UNSET
        else:
            grouping_explanation = IssueGroupingExplanation.from_dict(_grouping_explanation)

        issue_occurrence = cls(
            event_id=event_id,
            occurred_at=occurred_at,
            exception_type=exception_type,
            message=message,
            deployment_id=deployment_id,
            received_at=received_at,
            attribution=attribution,
            breadcrumbs=breadcrumbs,
            breadcrumbs_dropped=breadcrumbs_dropped,
            tags=tags,
            symbolication_strategy=symbolication_strategy,
            symbolication=symbolication,
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
            id=id,
            original_issue_id=original_issue_id,
            verified_consumer_id=verified_consumer_id,
            verified_platform_tenant_id=verified_platform_tenant_id,
            debug_request_id=debug_request_id,
            grouping_explanation=grouping_explanation,
        )

        issue_occurrence.additional_properties = d
        return issue_occurrence

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
