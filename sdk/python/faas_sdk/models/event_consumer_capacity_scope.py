from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_consumer_capacity_scope_scope import (
    EventConsumerCapacityScopeScope,
    check_event_consumer_capacity_scope_scope,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventConsumerCapacityScope")


@_attrs_define
class EventConsumerCapacityScope:
    """Current usage and ceiling together with this subscription's effective recorded waits for this scope. Advice is
    informational and never initiates recovery or changes controls.

    """

    scope: EventConsumerCapacityScopeScope
    live_deliveries: int
    """Trusted pending plus dispatching delivery slots, including replay descendants; excludes untagged legacy
    invocations."""
    effective_limit: int
    """Current plan ceiling, clamped by a configured subscription ceiling for consumer scope."""
    at_limit: bool
    """Live usage is greater than or equal to the effective ceiling."""
    waiting_recipients: int
    """This subscription's retained pending application recipients whose effective reason is capacity at this
    scope. Precedence follows the existing backlog pause, circuit, ordering and routing-state rules."""
    oldest_waiting_age_seconds: float
    """Age since acceptance at observed_at, not continuous capacity-wait duration; zero when none wait."""
    advice: str
    """Suggested read-only investigation or operator review based on current scope pressure."""
    oldest_waiting_accepted_at: datetime.datetime | Unset = UNSET
    """Acceptance timestamp of the oldest such recipient; absent when no recipients wait at this scope."""

    def to_dict(self) -> dict[str, Any]:
        scope: str = self.scope

        live_deliveries = self.live_deliveries

        effective_limit = self.effective_limit

        at_limit = self.at_limit

        waiting_recipients = self.waiting_recipients

        oldest_waiting_age_seconds = self.oldest_waiting_age_seconds

        advice = self.advice

        oldest_waiting_accepted_at: str | Unset = UNSET
        if not isinstance(self.oldest_waiting_accepted_at, Unset):
            oldest_waiting_accepted_at = self.oldest_waiting_accepted_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "scope": scope,
                "live_deliveries": live_deliveries,
                "effective_limit": effective_limit,
                "at_limit": at_limit,
                "waiting_recipients": waiting_recipients,
                "oldest_waiting_age_seconds": oldest_waiting_age_seconds,
                "advice": advice,
            }
        )
        if oldest_waiting_accepted_at is not UNSET:
            field_dict["oldest_waiting_accepted_at"] = oldest_waiting_accepted_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        scope = check_event_consumer_capacity_scope_scope(d.pop("scope"))

        live_deliveries = d.pop("live_deliveries")

        effective_limit = d.pop("effective_limit")

        at_limit = d.pop("at_limit")

        waiting_recipients = d.pop("waiting_recipients")

        oldest_waiting_age_seconds = d.pop("oldest_waiting_age_seconds")

        advice = d.pop("advice")

        _oldest_waiting_accepted_at = d.pop("oldest_waiting_accepted_at", UNSET)
        oldest_waiting_accepted_at: datetime.datetime | Unset
        if isinstance(_oldest_waiting_accepted_at, Unset):
            oldest_waiting_accepted_at = UNSET
        else:
            oldest_waiting_accepted_at = datetime.datetime.fromisoformat(_oldest_waiting_accepted_at)

        event_consumer_capacity_scope = cls(
            scope=scope,
            live_deliveries=live_deliveries,
            effective_limit=effective_limit,
            at_limit=at_limit,
            waiting_recipients=waiting_recipients,
            oldest_waiting_age_seconds=oldest_waiting_age_seconds,
            advice=advice,
            oldest_waiting_accepted_at=oldest_waiting_accepted_at,
        )

        return event_consumer_capacity_scope
