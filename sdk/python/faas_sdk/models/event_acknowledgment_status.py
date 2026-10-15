from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_acknowledgment_status_disposition import (
    EventAcknowledgmentStatusDisposition,
    check_event_acknowledgment_status_disposition,
)
from ..models.event_acknowledgment_status_processing_state import (
    EventAcknowledgmentStatusProcessingState,
    check_event_acknowledgment_status_processing_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventAcknowledgmentStatus")


@_attrs_define
class EventAcknowledgmentStatus:
    """Retained invocation and processing lease observation without secret token material."""

    invocation_id: UUID
    """Durable invocation receiving the deferred processing callback."""
    attempt: int
    """Current execution claim number for this processing observation; zero follows an in-place replay reset."""
    replay_generation: int
    """Current in-place replay generation fencing old delivery attempts."""
    state: str
    """Underlying durable invocation lifecycle state."""
    processing_state: EventAcknowledgmentStatusProcessingState
    """Processing phase derived from the retained claim and current database clock."""
    disposition: EventAcknowledgmentStatusDisposition | Unset = UNSET
    """Requested negative acknowledgment action; retry may exhaust its captured budget and dead-letter."""
    failure_reason: str | Unset = UNSET
    """Non-sensitive reason code retained for this attempt."""
    next_attempt_at: datetime.datetime | Unset = UNSET
    """Saved retry due time; absent for a terminal disposition."""
    lease_expires_at: datetime.datetime | Unset = UNSET
    """Current invocation lease expiry; absent after expiry recovery."""
    deadline_at: datetime.datetime | Unset = UNSET
    """Absolute one-hour processing deadline of the matching attempt."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        invocation_id = str(self.invocation_id)

        attempt = self.attempt

        replay_generation = self.replay_generation

        state = self.state

        processing_state: str = self.processing_state

        disposition: str | Unset = UNSET
        if not isinstance(self.disposition, Unset):
            disposition = self.disposition

        failure_reason = self.failure_reason

        next_attempt_at: str | Unset = UNSET
        if not isinstance(self.next_attempt_at, Unset):
            next_attempt_at = self.next_attempt_at.isoformat()

        lease_expires_at: str | Unset = UNSET
        if not isinstance(self.lease_expires_at, Unset):
            lease_expires_at = self.lease_expires_at.isoformat()

        deadline_at: str | Unset = UNSET
        if not isinstance(self.deadline_at, Unset):
            deadline_at = self.deadline_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "invocation_id": invocation_id,
                "attempt": attempt,
                "replay_generation": replay_generation,
                "state": state,
                "processing_state": processing_state,
            }
        )
        if disposition is not UNSET:
            field_dict["disposition"] = disposition
        if failure_reason is not UNSET:
            field_dict["failure_reason"] = failure_reason
        if next_attempt_at is not UNSET:
            field_dict["next_attempt_at"] = next_attempt_at
        if lease_expires_at is not UNSET:
            field_dict["lease_expires_at"] = lease_expires_at
        if deadline_at is not UNSET:
            field_dict["deadline_at"] = deadline_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        invocation_id = UUID(d.pop("invocation_id"))

        attempt = d.pop("attempt")

        replay_generation = d.pop("replay_generation")

        state = d.pop("state")

        processing_state = check_event_acknowledgment_status_processing_state(d.pop("processing_state"))

        _disposition = d.pop("disposition", UNSET)
        disposition: EventAcknowledgmentStatusDisposition | Unset
        if isinstance(_disposition, Unset):
            disposition = UNSET
        else:
            disposition = check_event_acknowledgment_status_disposition(_disposition)

        failure_reason = d.pop("failure_reason", UNSET)

        _next_attempt_at = d.pop("next_attempt_at", UNSET)
        next_attempt_at: datetime.datetime | Unset
        if isinstance(_next_attempt_at, Unset):
            next_attempt_at = UNSET
        else:
            next_attempt_at = datetime.datetime.fromisoformat(_next_attempt_at)

        _lease_expires_at = d.pop("lease_expires_at", UNSET)
        lease_expires_at: datetime.datetime | Unset
        if isinstance(_lease_expires_at, Unset):
            lease_expires_at = UNSET
        else:
            lease_expires_at = datetime.datetime.fromisoformat(_lease_expires_at)

        _deadline_at = d.pop("deadline_at", UNSET)
        deadline_at: datetime.datetime | Unset
        if isinstance(_deadline_at, Unset):
            deadline_at = UNSET
        else:
            deadline_at = datetime.datetime.fromisoformat(_deadline_at)

        event_acknowledgment_status = cls(
            invocation_id=invocation_id,
            attempt=attempt,
            replay_generation=replay_generation,
            state=state,
            processing_state=processing_state,
            disposition=disposition,
            failure_reason=failure_reason,
            next_attempt_at=next_attempt_at,
            lease_expires_at=lease_expires_at,
            deadline_at=deadline_at,
        )

        event_acknowledgment_status.additional_properties = d
        return event_acknowledgment_status

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
