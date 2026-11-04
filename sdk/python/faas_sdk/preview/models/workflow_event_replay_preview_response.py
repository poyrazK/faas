from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_event_replay_preview_response_coverage import (
    WorkflowEventReplayPreviewResponseCoverage,
    check_workflow_event_replay_preview_response_coverage,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_replay_preview_retention import EventReplayPreviewRetention
    from ..models.workflow_event_replay_preview_match import WorkflowEventReplayPreviewMatch


T = TypeVar("T", bound="WorkflowEventReplayPreviewResponse")


@_attrs_define
class WorkflowEventReplayPreviewResponse:
    """Read-only, bounded preview of captured workflow event recipients; potential admissions do not guarantee future
    workflow admission.

    """

    app_slug: str
    workflow_name: str
    from_: datetime.datetime
    until: datetime.datetime
    cutoff_at: datetime.datetime
    """Acceptance-time upper bound carried forward from the first page."""
    observed_at: datetime.datetime
    """Server timestamp when this workflow preview page was assembled."""
    coverage: WorkflowEventReplayPreviewResponseCoverage
    retention: EventReplayPreviewRetention
    """Existing receipt retention information without a complete archive guarantee."""
    scanned_count: int
    """Retained envelopes examined on this page."""
    captured_count: int
    """Envelopes whose original snapshot contains the named app workflow."""
    matched_count: int
    """Captured recipients whose immutable trigger filter matches the event."""
    filter_mismatch_count: int
    not_captured_count: int
    """Envelopes with a captured snapshot that excludes this workflow recipient."""
    unknown_recipient_count: int
    """Legacy envelopes without an original recipient snapshot."""
    already_admitted_count: int
    """Matching recipients with a durable admission receipt; these are deduplicated."""
    potential_admission_count: int
    """Matching recipients without an admission receipt; not a guarantee of future quota or target eligibility."""
    matches: list[WorkflowEventReplayPreviewMatch]
    next_after: str | Unset = UNSET
    """Continue even if this page has no matches; absent when no more currently retained envelopes follow."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        workflow_name = self.workflow_name

        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        cutoff_at = self.cutoff_at.isoformat()

        observed_at = self.observed_at.isoformat()

        coverage: str = self.coverage

        retention = self.retention.to_dict()

        scanned_count = self.scanned_count

        captured_count = self.captured_count

        matched_count = self.matched_count

        filter_mismatch_count = self.filter_mismatch_count

        not_captured_count = self.not_captured_count

        unknown_recipient_count = self.unknown_recipient_count

        already_admitted_count = self.already_admitted_count

        potential_admission_count = self.potential_admission_count

        matches = []
        for matches_item_data in self.matches:
            matches_item = matches_item_data.to_dict()
            matches.append(matches_item)

        next_after = self.next_after

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_slug": app_slug,
                "workflow_name": workflow_name,
                "from": from_,
                "until": until,
                "cutoff_at": cutoff_at,
                "observed_at": observed_at,
                "coverage": coverage,
                "retention": retention,
                "scanned_count": scanned_count,
                "captured_count": captured_count,
                "matched_count": matched_count,
                "filter_mismatch_count": filter_mismatch_count,
                "not_captured_count": not_captured_count,
                "unknown_recipient_count": unknown_recipient_count,
                "already_admitted_count": already_admitted_count,
                "potential_admission_count": potential_admission_count,
                "matches": matches,
            }
        )
        if next_after is not UNSET:
            field_dict["next_after"] = next_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_replay_preview_retention import EventReplayPreviewRetention
        from ..models.workflow_event_replay_preview_match import WorkflowEventReplayPreviewMatch

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        workflow_name = d.pop("workflow_name")

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        cutoff_at = datetime.datetime.fromisoformat(d.pop("cutoff_at"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        coverage = check_workflow_event_replay_preview_response_coverage(d.pop("coverage"))

        retention = EventReplayPreviewRetention.from_dict(d.pop("retention"))

        scanned_count = d.pop("scanned_count")

        captured_count = d.pop("captured_count")

        matched_count = d.pop("matched_count")

        filter_mismatch_count = d.pop("filter_mismatch_count")

        not_captured_count = d.pop("not_captured_count")

        unknown_recipient_count = d.pop("unknown_recipient_count")

        already_admitted_count = d.pop("already_admitted_count")

        potential_admission_count = d.pop("potential_admission_count")

        matches = []
        _matches = d.pop("matches")
        for matches_item_data in _matches:
            matches_item = WorkflowEventReplayPreviewMatch.from_dict(matches_item_data)

            matches.append(matches_item)

        next_after = d.pop("next_after", UNSET)

        workflow_event_replay_preview_response = cls(
            app_slug=app_slug,
            workflow_name=workflow_name,
            from_=from_,
            until=until,
            cutoff_at=cutoff_at,
            observed_at=observed_at,
            coverage=coverage,
            retention=retention,
            scanned_count=scanned_count,
            captured_count=captured_count,
            matched_count=matched_count,
            filter_mismatch_count=filter_mismatch_count,
            not_captured_count=not_captured_count,
            unknown_recipient_count=unknown_recipient_count,
            already_admitted_count=already_admitted_count,
            potential_admission_count=potential_admission_count,
            matches=matches,
            next_after=next_after,
        )

        workflow_event_replay_preview_response.additional_properties = d
        return workflow_event_replay_preview_response

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
