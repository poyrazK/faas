from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_routing_recovery_expectation import EventRoutingRecoveryExpectation


T = TypeVar("T", bound="ReplayEventFanoutFailureRequest")


@_attrs_define
class ReplayEventFanoutFailureRequest:
    """Identity of one terminal fanout failure to replay. Use either acceptance and generation guards or
    recovery_expectation; combining the guard formats is invalid.

    """

    event_id: str
    event_source: str
    subscription_id: UUID
    expected_generation: int | Unset = UNSET
    """Optional routing generation. Requires expected_accepted_at; checked with failed state under the recipient
    row lock. A changed generation returns 409 event_replay_generation_changed without mutation."""
    expected_accepted_at: str | Unset = UNSET
    """Optional exact original acceptance instant. Checked under the receipt row lock in the replay transaction;
    replacement returns 409 without mutation. Guarded replay targets the original acceptance snapshot, not
    backfills."""
    recovery_expectation: EventRoutingRecoveryExpectation | Unset = UNSET
    """Reviewed acceptance-time automation routing state. All comparisons occur in the replay transaction;
    mismatches return 409 event_recovery_changed without queuing a replay. Generation must be supplied for recipient
    routing and omitted for legacy routing. Does not bypass admission rules."""
    allow_expired: bool | Unset = False
    """Allow expired in the replay event fanout failure request: explicitly override delivery age for this replay
    generation or historical backfill job. Preserves deterministic invocation identity and manual controls."""

    def to_dict(self) -> dict[str, Any]:
        event_id = self.event_id

        event_source = self.event_source

        subscription_id = str(self.subscription_id)

        expected_generation = self.expected_generation

        expected_accepted_at: str | Unset = UNSET
        if not isinstance(self.expected_accepted_at, Unset):
            expected_accepted_at = self.expected_accepted_at

        recovery_expectation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.recovery_expectation, Unset):
            recovery_expectation = self.recovery_expectation.to_dict()

        allow_expired = self.allow_expired

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "event_id": event_id,
                "event_source": event_source,
                "subscription_id": subscription_id,
            }
        )
        if expected_generation is not UNSET:
            field_dict["expected_generation"] = expected_generation
        if expected_accepted_at is not UNSET:
            field_dict["expected_accepted_at"] = expected_accepted_at
        if recovery_expectation is not UNSET:
            field_dict["recovery_expectation"] = recovery_expectation
        if allow_expired is not UNSET:
            field_dict["allow_expired"] = allow_expired

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_routing_recovery_expectation import EventRoutingRecoveryExpectation

        d = dict(src_dict)
        event_id = d.pop("event_id")

        event_source = d.pop("event_source")

        subscription_id = UUID(d.pop("subscription_id"))

        expected_generation = d.pop("expected_generation", UNSET)

        _expected_accepted_at = d.pop("expected_accepted_at", UNSET)
        expected_accepted_at: str | Unset
        if isinstance(_expected_accepted_at, Unset):
            expected_accepted_at = UNSET
        else:
            expected_accepted_at = _expected_accepted_at

        _recovery_expectation = d.pop("recovery_expectation", UNSET)
        recovery_expectation: EventRoutingRecoveryExpectation | Unset
        if isinstance(_recovery_expectation, Unset):
            recovery_expectation = UNSET
        else:
            recovery_expectation = EventRoutingRecoveryExpectation.from_dict(_recovery_expectation)

        allow_expired = d.pop("allow_expired", UNSET)

        replay_event_fanout_failure_request = cls(
            event_id=event_id,
            event_source=event_source,
            subscription_id=subscription_id,
            expected_generation=expected_generation,
            expected_accepted_at=expected_accepted_at,
            recovery_expectation=recovery_expectation,
            allow_expired=allow_expired,
        )

        return replay_event_fanout_failure_request
