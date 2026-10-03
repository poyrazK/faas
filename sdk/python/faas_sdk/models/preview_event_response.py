from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.event_preview_subscription import EventPreviewSubscription


T = TypeVar("T", bound="PreviewEventResponse")


@_attrs_define
class PreviewEventResponse:
    """Read-only event-routing preview; counts cover all candidates while lists are bounded samples."""

    event_id: str
    source: str
    type_: str
    candidate_count: int
    """Enabled source/type candidates considered."""
    matched_count: int
    """Subscriptions that would receive the event."""
    filter_mismatch_count: int
    """Candidates excluded by their content filters."""
    other_mismatch_count: int
    """Candidates rejected for another reason, including invalid stored subscription data."""
    matches: list[EventPreviewSubscription]
    non_matches: list[EventPreviewSubscription]
    truncated: bool
    """True when either sample omits additional candidates."""

    def to_dict(self) -> dict[str, Any]:
        event_id = self.event_id

        source = self.source

        type_ = self.type_

        candidate_count = self.candidate_count

        matched_count = self.matched_count

        filter_mismatch_count = self.filter_mismatch_count

        other_mismatch_count = self.other_mismatch_count

        matches = []
        for matches_item_data in self.matches:
            matches_item = matches_item_data.to_dict()
            matches.append(matches_item)

        non_matches = []
        for non_matches_item_data in self.non_matches:
            non_matches_item = non_matches_item_data.to_dict()
            non_matches.append(non_matches_item)

        truncated = self.truncated

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_id": event_id,
                "source": source,
                "type": type_,
                "candidate_count": candidate_count,
                "matched_count": matched_count,
                "filter_mismatch_count": filter_mismatch_count,
                "other_mismatch_count": other_mismatch_count,
                "matches": matches,
                "non_matches": non_matches,
                "truncated": truncated,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_preview_subscription import EventPreviewSubscription

        d = dict(src_dict)
        event_id = d.pop("event_id")

        source = d.pop("source")

        type_ = d.pop("type")

        candidate_count = d.pop("candidate_count")

        matched_count = d.pop("matched_count")

        filter_mismatch_count = d.pop("filter_mismatch_count")

        other_mismatch_count = d.pop("other_mismatch_count")

        matches = []
        _matches = d.pop("matches")
        for matches_item_data in _matches:
            matches_item = EventPreviewSubscription.from_dict(matches_item_data)

            matches.append(matches_item)

        non_matches = []
        _non_matches = d.pop("non_matches")
        for non_matches_item_data in _non_matches:
            non_matches_item = EventPreviewSubscription.from_dict(non_matches_item_data)

            non_matches.append(non_matches_item)

        truncated = d.pop("truncated")

        preview_event_response = cls(
            event_id=event_id,
            source=source,
            type_=type_,
            candidate_count=candidate_count,
            matched_count=matched_count,
            filter_mismatch_count=filter_mismatch_count,
            other_mismatch_count=other_mismatch_count,
            matches=matches,
            non_matches=non_matches,
            truncated=truncated,
        )

        return preview_event_response
