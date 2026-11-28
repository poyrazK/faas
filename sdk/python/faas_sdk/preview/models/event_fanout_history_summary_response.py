from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_fanout_history_summary_response_last_capacity_scope import (
    EventFanoutHistorySummaryResponseLastCapacityScope,
    check_event_fanout_history_summary_response_last_capacity_scope,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventFanoutHistorySummaryResponse")


@_attrs_define
class EventFanoutHistorySummaryResponse:
    """Durable recipient counters and detail coverage for the retained receipt."""

    subscription_id: UUID
    observed_outcomes: int
    """Recorded routing and replay observations including coalesced waits."""
    capacity_deferrals: int
    """Highest durable cumulative recipient deferral checkpoint."""
    coalesced_outcomes: int
    """Repeated waits represented by counters instead of detail rows."""
    compacted_outcomes: int
    """Detail rows removed by retention or budgets."""
    retained_records: int
    retained_bytes: int
    """Logical detail bytes including a fixed per-row allowance; excludes physical storage and indexes."""
    compacted_through_id: int | Unset = UNSET
    """Highest removed detail ID; protected older rows can remain, so this is not a contiguous missing prefix."""
    compacted_through_at: datetime.datetime | Unset = UNSET
    """Latest occurrence time among removed detail rows."""
    first_capacity_wait_at: datetime.datetime | Unset = UNSET
    """First recorded capacity wait after summary rollout."""
    last_capacity_wait_at: datetime.datetime | Unset = UNSET
    last_capacity_scope: EventFanoutHistorySummaryResponseLastCapacityScope | Unset = UNSET
    """Most recent recorded capacity scope, also retained after recovery."""

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        observed_outcomes = self.observed_outcomes

        capacity_deferrals = self.capacity_deferrals

        coalesced_outcomes = self.coalesced_outcomes

        compacted_outcomes = self.compacted_outcomes

        retained_records = self.retained_records

        retained_bytes = self.retained_bytes

        compacted_through_id = self.compacted_through_id

        compacted_through_at: str | Unset = UNSET
        if not isinstance(self.compacted_through_at, Unset):
            compacted_through_at = self.compacted_through_at.isoformat()

        first_capacity_wait_at: str | Unset = UNSET
        if not isinstance(self.first_capacity_wait_at, Unset):
            first_capacity_wait_at = self.first_capacity_wait_at.isoformat()

        last_capacity_wait_at: str | Unset = UNSET
        if not isinstance(self.last_capacity_wait_at, Unset):
            last_capacity_wait_at = self.last_capacity_wait_at.isoformat()

        last_capacity_scope: str | Unset = UNSET
        if not isinstance(self.last_capacity_scope, Unset):
            last_capacity_scope = self.last_capacity_scope

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "subscription_id": subscription_id,
                "observed_outcomes": observed_outcomes,
                "capacity_deferrals": capacity_deferrals,
                "coalesced_outcomes": coalesced_outcomes,
                "compacted_outcomes": compacted_outcomes,
                "retained_records": retained_records,
                "retained_bytes": retained_bytes,
            }
        )
        if compacted_through_id is not UNSET:
            field_dict["compacted_through_id"] = compacted_through_id
        if compacted_through_at is not UNSET:
            field_dict["compacted_through_at"] = compacted_through_at
        if first_capacity_wait_at is not UNSET:
            field_dict["first_capacity_wait_at"] = first_capacity_wait_at
        if last_capacity_wait_at is not UNSET:
            field_dict["last_capacity_wait_at"] = last_capacity_wait_at
        if last_capacity_scope is not UNSET:
            field_dict["last_capacity_scope"] = last_capacity_scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        observed_outcomes = d.pop("observed_outcomes")

        capacity_deferrals = d.pop("capacity_deferrals")

        coalesced_outcomes = d.pop("coalesced_outcomes")

        compacted_outcomes = d.pop("compacted_outcomes")

        retained_records = d.pop("retained_records")

        retained_bytes = d.pop("retained_bytes")

        compacted_through_id = d.pop("compacted_through_id", UNSET)

        _compacted_through_at = d.pop("compacted_through_at", UNSET)
        compacted_through_at: datetime.datetime | Unset
        if isinstance(_compacted_through_at, Unset):
            compacted_through_at = UNSET
        else:
            compacted_through_at = datetime.datetime.fromisoformat(_compacted_through_at)

        _first_capacity_wait_at = d.pop("first_capacity_wait_at", UNSET)
        first_capacity_wait_at: datetime.datetime | Unset
        if isinstance(_first_capacity_wait_at, Unset):
            first_capacity_wait_at = UNSET
        else:
            first_capacity_wait_at = datetime.datetime.fromisoformat(_first_capacity_wait_at)

        _last_capacity_wait_at = d.pop("last_capacity_wait_at", UNSET)
        last_capacity_wait_at: datetime.datetime | Unset
        if isinstance(_last_capacity_wait_at, Unset):
            last_capacity_wait_at = UNSET
        else:
            last_capacity_wait_at = datetime.datetime.fromisoformat(_last_capacity_wait_at)

        _last_capacity_scope = d.pop("last_capacity_scope", UNSET)
        last_capacity_scope: EventFanoutHistorySummaryResponseLastCapacityScope | Unset
        if isinstance(_last_capacity_scope, Unset):
            last_capacity_scope = UNSET
        else:
            last_capacity_scope = check_event_fanout_history_summary_response_last_capacity_scope(_last_capacity_scope)

        event_fanout_history_summary_response = cls(
            subscription_id=subscription_id,
            observed_outcomes=observed_outcomes,
            capacity_deferrals=capacity_deferrals,
            coalesced_outcomes=coalesced_outcomes,
            compacted_outcomes=compacted_outcomes,
            retained_records=retained_records,
            retained_bytes=retained_bytes,
            compacted_through_id=compacted_through_id,
            compacted_through_at=compacted_through_at,
            first_capacity_wait_at=first_capacity_wait_at,
            last_capacity_wait_at=last_capacity_wait_at,
            last_capacity_scope=last_capacity_scope,
        )

        return event_fanout_history_summary_response
