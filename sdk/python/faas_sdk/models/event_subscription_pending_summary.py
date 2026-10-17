from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_subscription_pending_summary_capacity_blocking_scopes_item import (
    EventSubscriptionPendingSummaryCapacityBlockingScopesItem,
    check_event_subscription_pending_summary_capacity_blocking_scopes_item,
)
from ..models.event_subscription_pending_summary_circuit_state import (
    EventSubscriptionPendingSummaryCircuitState,
    check_event_subscription_pending_summary_circuit_state,
)
from ..models.event_subscription_pending_summary_constraints_item import (
    EventSubscriptionPendingSummaryConstraintsItem,
    check_event_subscription_pending_summary_constraints_item,
)
from ..models.event_subscription_pending_summary_coverage import (
    EventSubscriptionPendingSummaryCoverage,
    check_event_subscription_pending_summary_coverage,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_subscription_rate_limit import EventSubscriptionRateLimit


T = TypeVar("T", bound="EventSubscriptionPendingSummary")


@_attrs_define
class EventSubscriptionPendingSummary:
    """Full retained captured pending application-recipient counts from one read-only snapshot. No payloads, identities or
    modeled completion estimates. Incomplete evidence cannot establish health or recovery.

    """

    coverage: EventSubscriptionPendingSummaryCoverage
    app_id: UUID
    subscription_id: UUID
    observed_at: datetime.datetime
    read_only: bool
    complete: bool
    """False for unusable clocks; incomplete counts must not drive alert recovery."""
    pending_recipients: int
    deadline_recipients: int
    already_expired_count: int
    current: EventSubscriptionRateLimit
    """Projected token-bucket capacity at observed_at. Read-only metadata reserves no tokens. Missing rate_limit on
    older responses means unknown."""
    paused: bool
    legacy_rate_per_second: int
    circuit_state: EventSubscriptionPendingSummaryCircuitState
    capacity_blocking_scopes: list[EventSubscriptionPendingSummaryCapacityBlockingScopesItem]
    constraints: list[EventSubscriptionPendingSummaryConstraintsItem]
    earliest_deadline_at: datetime.datetime | Unset = UNSET
    """Earliest effective deadline, including expired rows; omitted when no pending recipients have deadlines."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        coverage: str = self.coverage

        app_id = str(self.app_id)

        subscription_id = str(self.subscription_id)

        observed_at = self.observed_at.isoformat()

        read_only = self.read_only

        complete = self.complete

        pending_recipients = self.pending_recipients

        deadline_recipients = self.deadline_recipients

        already_expired_count = self.already_expired_count

        current = self.current.to_dict()

        paused = self.paused

        legacy_rate_per_second = self.legacy_rate_per_second

        circuit_state: str = self.circuit_state

        capacity_blocking_scopes = []
        for capacity_blocking_scopes_item_data in self.capacity_blocking_scopes:
            capacity_blocking_scopes_item: str = capacity_blocking_scopes_item_data
            capacity_blocking_scopes.append(capacity_blocking_scopes_item)

        constraints = []
        for constraints_item_data in self.constraints:
            constraints_item: str = constraints_item_data
            constraints.append(constraints_item)

        earliest_deadline_at: str | Unset = UNSET
        if not isinstance(self.earliest_deadline_at, Unset):
            earliest_deadline_at = self.earliest_deadline_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "coverage": coverage,
                "app_id": app_id,
                "subscription_id": subscription_id,
                "observed_at": observed_at,
                "read_only": read_only,
                "complete": complete,
                "pending_recipients": pending_recipients,
                "deadline_recipients": deadline_recipients,
                "already_expired_count": already_expired_count,
                "current": current,
                "paused": paused,
                "legacy_rate_per_second": legacy_rate_per_second,
                "circuit_state": circuit_state,
                "capacity_blocking_scopes": capacity_blocking_scopes,
                "constraints": constraints,
            }
        )
        if earliest_deadline_at is not UNSET:
            field_dict["earliest_deadline_at"] = earliest_deadline_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_subscription_rate_limit import EventSubscriptionRateLimit

        d = dict(src_dict)
        coverage = check_event_subscription_pending_summary_coverage(d.pop("coverage"))

        app_id = UUID(d.pop("app_id"))

        subscription_id = UUID(d.pop("subscription_id"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        read_only = d.pop("read_only")

        complete = d.pop("complete")

        pending_recipients = d.pop("pending_recipients")

        deadline_recipients = d.pop("deadline_recipients")

        already_expired_count = d.pop("already_expired_count")

        current = EventSubscriptionRateLimit.from_dict(d.pop("current"))

        paused = d.pop("paused")

        legacy_rate_per_second = d.pop("legacy_rate_per_second")

        circuit_state = check_event_subscription_pending_summary_circuit_state(d.pop("circuit_state"))

        capacity_blocking_scopes = []
        _capacity_blocking_scopes = d.pop("capacity_blocking_scopes")
        for capacity_blocking_scopes_item_data in _capacity_blocking_scopes:
            capacity_blocking_scopes_item = check_event_subscription_pending_summary_capacity_blocking_scopes_item(
                capacity_blocking_scopes_item_data
            )

            capacity_blocking_scopes.append(capacity_blocking_scopes_item)

        constraints = []
        _constraints = d.pop("constraints")
        for constraints_item_data in _constraints:
            constraints_item = check_event_subscription_pending_summary_constraints_item(constraints_item_data)

            constraints.append(constraints_item)

        _earliest_deadline_at = d.pop("earliest_deadline_at", UNSET)
        earliest_deadline_at: datetime.datetime | Unset
        if isinstance(_earliest_deadline_at, Unset):
            earliest_deadline_at = UNSET
        else:
            earliest_deadline_at = datetime.datetime.fromisoformat(_earliest_deadline_at)

        event_subscription_pending_summary = cls(
            coverage=coverage,
            app_id=app_id,
            subscription_id=subscription_id,
            observed_at=observed_at,
            read_only=read_only,
            complete=complete,
            pending_recipients=pending_recipients,
            deadline_recipients=deadline_recipients,
            already_expired_count=already_expired_count,
            current=current,
            paused=paused,
            legacy_rate_per_second=legacy_rate_per_second,
            circuit_state=circuit_state,
            capacity_blocking_scopes=capacity_blocking_scopes,
            constraints=constraints,
            earliest_deadline_at=earliest_deadline_at,
        )

        event_subscription_pending_summary.additional_properties = d
        return event_subscription_pending_summary

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
