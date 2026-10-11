from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_replay_preview_response_coverage import (
    EventReplayPreviewResponseCoverage,
    check_event_replay_preview_response_coverage,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_replay_preview_match import EventReplayPreviewMatch
    from ..models.event_replay_preview_retention import EventReplayPreviewRetention
    from ..models.event_subscription_response import EventSubscriptionResponse


T = TypeVar("T", bound="EventReplayPreviewResponse")


@_attrs_define
class EventReplayPreviewResponse:
    """Read-only current-subscription preview of one bounded retained-event page."""

    app_slug: str
    subscription: EventSubscriptionResponse
    """One manifest-declared event subscription reconciled for an app."""
    subscription_revision: str
    """Opaque fingerprint of the current subscription declaration."""
    from_: datetime.datetime
    until: datetime.datetime
    cutoff_at: datetime.datetime
    """Fixed exclusive acceptance cutoff anchored on the first page."""
    observed_at: datetime.datetime
    """Observation time for this page."""
    coverage: EventReplayPreviewResponseCoverage
    retention: EventReplayPreviewRetention
    """Existing receipt retention information without a complete archive guarantee."""
    scanned_count: int
    """Envelopes examined on this page."""
    matched_count: int
    filter_mismatch_count: int
    pattern_mismatch_count: int
    already_captured_count: int
    """Matching events whose original snapshot contains this target."""
    matches: list[EventReplayPreviewMatch]
    next_after: str | Unset = UNSET
    """Continue even if this page has no matches; absent when no more currently retained candidates follow."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        subscription = self.subscription.to_dict()

        subscription_revision = self.subscription_revision

        from_ = self.from_.isoformat()

        until = self.until.isoformat()

        cutoff_at = self.cutoff_at.isoformat()

        observed_at = self.observed_at.isoformat()

        coverage: str = self.coverage

        retention = self.retention.to_dict()

        scanned_count = self.scanned_count

        matched_count = self.matched_count

        filter_mismatch_count = self.filter_mismatch_count

        pattern_mismatch_count = self.pattern_mismatch_count

        already_captured_count = self.already_captured_count

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
                "subscription": subscription,
                "subscription_revision": subscription_revision,
                "from": from_,
                "until": until,
                "cutoff_at": cutoff_at,
                "observed_at": observed_at,
                "coverage": coverage,
                "retention": retention,
                "scanned_count": scanned_count,
                "matched_count": matched_count,
                "filter_mismatch_count": filter_mismatch_count,
                "pattern_mismatch_count": pattern_mismatch_count,
                "already_captured_count": already_captured_count,
                "matches": matches,
            }
        )
        if next_after is not UNSET:
            field_dict["next_after"] = next_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_replay_preview_match import EventReplayPreviewMatch
        from ..models.event_replay_preview_retention import EventReplayPreviewRetention
        from ..models.event_subscription_response import EventSubscriptionResponse

        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        subscription = EventSubscriptionResponse.from_dict(d.pop("subscription"))

        subscription_revision = d.pop("subscription_revision")

        from_ = datetime.datetime.fromisoformat(d.pop("from"))

        until = datetime.datetime.fromisoformat(d.pop("until"))

        cutoff_at = datetime.datetime.fromisoformat(d.pop("cutoff_at"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        coverage = check_event_replay_preview_response_coverage(d.pop("coverage"))

        retention = EventReplayPreviewRetention.from_dict(d.pop("retention"))

        scanned_count = d.pop("scanned_count")

        matched_count = d.pop("matched_count")

        filter_mismatch_count = d.pop("filter_mismatch_count")

        pattern_mismatch_count = d.pop("pattern_mismatch_count")

        already_captured_count = d.pop("already_captured_count")

        matches = []
        _matches = d.pop("matches")
        for matches_item_data in _matches:
            matches_item = EventReplayPreviewMatch.from_dict(matches_item_data)

            matches.append(matches_item)

        next_after = d.pop("next_after", UNSET)

        event_replay_preview_response = cls(
            app_slug=app_slug,
            subscription=subscription,
            subscription_revision=subscription_revision,
            from_=from_,
            until=until,
            cutoff_at=cutoff_at,
            observed_at=observed_at,
            coverage=coverage,
            retention=retention,
            scanned_count=scanned_count,
            matched_count=matched_count,
            filter_mismatch_count=filter_mismatch_count,
            pattern_mismatch_count=pattern_mismatch_count,
            already_captured_count=already_captured_count,
            matches=matches,
            next_after=next_after,
        )

        event_replay_preview_response.additional_properties = d
        return event_replay_preview_response

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
