from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_acknowledgment_history_coverage import (
    EventAcknowledgmentHistoryCoverage,
    check_event_acknowledgment_history_coverage,
)

if TYPE_CHECKING:
    from ..models.event_acknowledgment_outcome import EventAcknowledgmentOutcome


T = TypeVar("T", bound="EventAcknowledgmentHistory")


@_attrs_define
class EventAcknowledgmentHistory:
    """Most recently updated retained outcome groups for a scoped consumer."""

    outcomes: list[EventAcknowledgmentOutcome]
    """Outcome groups ordered by latest occurrence descending with stable tie breakers."""
    truncated: bool
    """More retained outcome groups existed at the read snapshot."""
    coverage: EventAcknowledgmentHistoryCoverage
    """Diagnostic retained invocation coverage since the ledger rollout."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        outcomes = []
        for outcomes_item_data in self.outcomes:
            outcomes_item = outcomes_item_data.to_dict()
            outcomes.append(outcomes_item)

        truncated = self.truncated

        coverage: str = self.coverage

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "outcomes": outcomes,
                "truncated": truncated,
                "coverage": coverage,
            }
        )

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

        truncated = d.pop("truncated")

        coverage = check_event_acknowledgment_history_coverage(d.pop("coverage"))

        event_acknowledgment_history = cls(
            outcomes=outcomes,
            truncated=truncated,
            coverage=coverage,
        )

        event_acknowledgment_history.additional_properties = d
        return event_acknowledgment_history

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
