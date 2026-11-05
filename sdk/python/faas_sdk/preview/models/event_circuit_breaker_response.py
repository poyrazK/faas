from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_circuit_breaker_response_state import (
    EventCircuitBreakerResponseState,
    check_event_circuit_breaker_response_state,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_circuit_breaker_policy import EventCircuitBreakerPolicy


T = TypeVar("T", bound="EventCircuitBreakerResponse")


@_attrs_define
class EventCircuitBreakerResponse:
    """Captured circuit policy and current breaker state for one application event subscription."""

    subscription_id: UUID
    enabled: bool
    state: EventCircuitBreakerResponseState
    probe_in_flight: bool
    successful_probes: int
    recovery_rate_per_second: int
    history_incomplete: bool
    """Compacted history prevents automatic threshold decisions or completion of recovery."""
    manual_paused: bool
    policy: EventCircuitBreakerPolicy | Unset = UNSET
    """Opt-in routing circuit breaker. Omitted properties use defaults. Failures and successful admissions form the
    sample; capacity waits and filtered events are excluded. Retained history must cover the observation window."""
    reason: str | Unset = UNSET
    """Reason for the last durable transition."""
    changed_at: datetime.datetime | Unset = UNSET
    cooldown_until: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        enabled = self.enabled

        state: str = self.state

        probe_in_flight = self.probe_in_flight

        successful_probes = self.successful_probes

        recovery_rate_per_second = self.recovery_rate_per_second

        history_incomplete = self.history_incomplete

        manual_paused = self.manual_paused

        policy: dict[str, Any] | Unset = UNSET
        if not isinstance(self.policy, Unset):
            policy = self.policy.to_dict()

        reason = self.reason

        changed_at: str | Unset = UNSET
        if not isinstance(self.changed_at, Unset):
            changed_at = self.changed_at.isoformat()

        cooldown_until: str | Unset = UNSET
        if not isinstance(self.cooldown_until, Unset):
            cooldown_until = self.cooldown_until.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription_id": subscription_id,
                "enabled": enabled,
                "state": state,
                "probe_in_flight": probe_in_flight,
                "successful_probes": successful_probes,
                "recovery_rate_per_second": recovery_rate_per_second,
                "history_incomplete": history_incomplete,
                "manual_paused": manual_paused,
            }
        )
        if policy is not UNSET:
            field_dict["policy"] = policy
        if reason is not UNSET:
            field_dict["reason"] = reason
        if changed_at is not UNSET:
            field_dict["changed_at"] = changed_at
        if cooldown_until is not UNSET:
            field_dict["cooldown_until"] = cooldown_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_circuit_breaker_policy import EventCircuitBreakerPolicy

        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        enabled = d.pop("enabled")

        state = check_event_circuit_breaker_response_state(d.pop("state"))

        probe_in_flight = d.pop("probe_in_flight")

        successful_probes = d.pop("successful_probes")

        recovery_rate_per_second = d.pop("recovery_rate_per_second")

        history_incomplete = d.pop("history_incomplete")

        manual_paused = d.pop("manual_paused")

        _policy = d.pop("policy", UNSET)
        policy: EventCircuitBreakerPolicy | Unset
        if isinstance(_policy, Unset):
            policy = UNSET
        else:
            policy = EventCircuitBreakerPolicy.from_dict(_policy)

        reason = d.pop("reason", UNSET)

        _changed_at = d.pop("changed_at", UNSET)
        changed_at: datetime.datetime | Unset
        if isinstance(_changed_at, Unset):
            changed_at = UNSET
        else:
            changed_at = datetime.datetime.fromisoformat(_changed_at)

        _cooldown_until = d.pop("cooldown_until", UNSET)
        cooldown_until: datetime.datetime | Unset
        if isinstance(_cooldown_until, Unset):
            cooldown_until = UNSET
        else:
            cooldown_until = datetime.datetime.fromisoformat(_cooldown_until)

        event_circuit_breaker_response = cls(
            subscription_id=subscription_id,
            enabled=enabled,
            state=state,
            probe_in_flight=probe_in_flight,
            successful_probes=successful_probes,
            recovery_rate_per_second=recovery_rate_per_second,
            history_incomplete=history_incomplete,
            manual_paused=manual_paused,
            policy=policy,
            reason=reason,
            changed_at=changed_at,
            cooldown_until=cooldown_until,
        )

        return event_circuit_breaker_response
