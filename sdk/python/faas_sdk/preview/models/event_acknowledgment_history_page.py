from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_acknowledgment_history_page_coverage import (
    EventAcknowledgmentHistoryPageCoverage,
    check_event_acknowledgment_history_page_coverage,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_acknowledgment_outcome import EventAcknowledgmentOutcome


T = TypeVar("T", bound="EventAcknowledgmentHistoryPage")


@_attrs_define
class EventAcknowledgmentHistoryPage:
    """Bounded page of coalesced outcomes ordered by immutable group identity."""

    outcomes: list[EventAcknowledgmentOutcome]
    """Groups ordered by first_at, invocation UUID, attempt, generation and kind descending."""
    has_more: bool
    """Another retained matching group existed in this read."""
    cutoff: datetime.datetime
    """Fixed initial read time excluding groups with later first occurrences."""
    coverage: EventAcknowledgmentHistoryPageCoverage
    """Retained diagnostic groups with live aggregate values and no historical snapshot guarantee."""
    next_cursor: str | Unset = UNSET
    """Continuation emitted only when has_more is true."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        outcomes = []
        for outcomes_item_data in self.outcomes:
            outcomes_item = outcomes_item_data.to_dict()
            outcomes.append(outcomes_item)

        has_more = self.has_more

        cutoff = self.cutoff.isoformat()

        coverage: str = self.coverage

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "outcomes": outcomes,
                "has_more": has_more,
                "cutoff": cutoff,
                "coverage": coverage,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_acknowledgment_outcome import EventAcknowledgmentOutcome

        d = dict(src_dict)
        outcomes = []
        _outcomes = d.pop("outcomes")
        for outcomes_item_data in _outcomes:
            outcomes_item = EventAcknowledgmentOutcome.from_dict(outcomes_item_data)

            outcomes.append(outcomes_item)

        has_more = d.pop("has_more")

        cutoff = datetime.datetime.fromisoformat(d.pop("cutoff"))

        coverage = check_event_acknowledgment_history_page_coverage(d.pop("coverage"))

        next_cursor = d.pop("next_cursor", UNSET)

        event_acknowledgment_history_page = cls(
            outcomes=outcomes,
            has_more=has_more,
            cutoff=cutoff,
            coverage=coverage,
            next_cursor=next_cursor,
        )

        event_acknowledgment_history_page.additional_properties = d
        return event_acknowledgment_history_page

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
