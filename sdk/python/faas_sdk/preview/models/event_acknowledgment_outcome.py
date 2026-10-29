from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_acknowledgment_outcome_kind import (
    EventAcknowledgmentOutcomeKind,
    check_event_acknowledgment_outcome_kind,
)

T = TypeVar("T", bound="EventAcknowledgmentOutcome")


@_attrs_define
class EventAcknowledgmentOutcome:
    """Payload-free coalesced outcome for one deferred delivery attempt."""

    invocation_id: UUID
    """Delivery whose attempt produced this outcome."""
    attempt: int
    """Fenced invocation attempt number."""
    replay_generation: int
    """Replay generation fenced by this outcome."""
    kind: EventAcknowledgmentOutcomeKind
    """Deferred acknowledgment operation or scheduler expiry outcome."""
    first_at: datetime.datetime
    """Database time of the first retained occurrence."""
    last_at: datetime.datetime
    """Database time of the latest retained occurrence."""
    occurrences: int
    """Coalesced count saturating at the documented maximum."""
    retain_until: datetime.datetime
    """Fixed history expiry anchored to the attempt deadline."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        invocation_id = str(self.invocation_id)

        attempt = self.attempt

        replay_generation = self.replay_generation

        kind: str = self.kind

        first_at = self.first_at.isoformat()

        last_at = self.last_at.isoformat()

        occurrences = self.occurrences

        retain_until = self.retain_until.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "invocation_id": invocation_id,
                "attempt": attempt,
                "replay_generation": replay_generation,
                "kind": kind,
                "first_at": first_at,
                "last_at": last_at,
                "occurrences": occurrences,
                "retain_until": retain_until,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        invocation_id = UUID(d.pop("invocation_id"))

        attempt = d.pop("attempt")

        replay_generation = d.pop("replay_generation")

        kind = check_event_acknowledgment_outcome_kind(d.pop("kind"))

        first_at = datetime.datetime.fromisoformat(d.pop("first_at"))

        last_at = datetime.datetime.fromisoformat(d.pop("last_at"))

        occurrences = d.pop("occurrences")

        retain_until = datetime.datetime.fromisoformat(d.pop("retain_until"))

        event_acknowledgment_outcome = cls(
            invocation_id=invocation_id,
            attempt=attempt,
            replay_generation=replay_generation,
            kind=kind,
            first_at=first_at,
            last_at=last_at,
            occurrences=occurrences,
            retain_until=retain_until,
        )

        event_acknowledgment_outcome.additional_properties = d
        return event_acknowledgment_outcome

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
