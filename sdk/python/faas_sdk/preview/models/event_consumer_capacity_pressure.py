from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_consumer_capacity_pressure_blocking_scope import (
    EventConsumerCapacityPressureBlockingScope,
    check_event_consumer_capacity_pressure_blocking_scope,
)
from ..models.event_consumer_capacity_pressure_coverage import (
    EventConsumerCapacityPressureCoverage,
    check_event_consumer_capacity_pressure_coverage,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_consumer_capacity_scope import EventConsumerCapacityScope
    from ..models.event_subscription_capacity_command_budget import EventSubscriptionCapacityCommandBudget


T = TypeVar("T", bound="EventConsumerCapacityPressure")


@_attrs_define
class EventConsumerCapacityPressure:
    """Read-only current live slot usage and effective capacity waiting reasons from one owned subscription snapshot.
    Independent of the other delivery-health reads and history window. Recorded waits may remain after capacity drains.
    Age is since event acceptance, not continuous capacity-wait duration. No capacity reservation or delivery-order
    guarantee.

    """

    plan_max_pending_deliveries: int
    """Current plan consumer ceiling before applying the saved subscription setting; used to validate proposed
    capacity changes."""
    app_id: UUID
    subscription_id: UUID
    observed_at: datetime.datetime
    coverage: EventConsumerCapacityPressureCoverage
    complete: bool
    """All trusted live slots and retained pending application recipients in this scope were assessed; not inferred
    from a bounded sample."""
    blocking_scope: EventConsumerCapacityPressureBlockingScope
    """Current first full admission ceiling in consumer then app then account order; empty means none observed. Not
    inferred from recorded waits."""
    configured_max_pending_deliveries: int
    """Saved subscription ceiling; zero uses the current plan default."""
    scopes: list[EventConsumerCapacityScope]
    """Consumer, app and account scopes in admission priority order. Waiting counts refer only to this
    subscription; live app/account counts include sibling deliveries in those scopes."""
    capacity_command_budget: EventSubscriptionCapacityCommandBudget | Unset = UNSET
    """Complete scoped advisory observation of all unexpired successful capacity-command receipts. Neither reserves
    receipt slots nor modifies retention. Known unchanged-input command repeats remain available when full; commands
    without UUIDs have no idempotent receipt evidence."""

    def to_dict(self) -> dict[str, Any]:
        plan_max_pending_deliveries = self.plan_max_pending_deliveries

        app_id = str(self.app_id)

        subscription_id = str(self.subscription_id)

        observed_at = self.observed_at.isoformat()

        coverage: str = self.coverage

        complete = self.complete

        blocking_scope: str = self.blocking_scope

        configured_max_pending_deliveries = self.configured_max_pending_deliveries

        scopes = []
        for scopes_item_data in self.scopes:
            scopes_item = scopes_item_data.to_dict()
            scopes.append(scopes_item)

        capacity_command_budget: dict[str, Any] | Unset = UNSET
        if not isinstance(self.capacity_command_budget, Unset):
            capacity_command_budget = self.capacity_command_budget.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "plan_max_pending_deliveries": plan_max_pending_deliveries,
                "app_id": app_id,
                "subscription_id": subscription_id,
                "observed_at": observed_at,
                "coverage": coverage,
                "complete": complete,
                "blocking_scope": blocking_scope,
                "configured_max_pending_deliveries": configured_max_pending_deliveries,
                "scopes": scopes,
            }
        )
        if capacity_command_budget is not UNSET:
            field_dict["capacity_command_budget"] = capacity_command_budget

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_consumer_capacity_scope import EventConsumerCapacityScope
        from ..models.event_subscription_capacity_command_budget import EventSubscriptionCapacityCommandBudget

        d = dict(src_dict)
        plan_max_pending_deliveries = d.pop("plan_max_pending_deliveries")

        app_id = UUID(d.pop("app_id"))

        subscription_id = UUID(d.pop("subscription_id"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        coverage = check_event_consumer_capacity_pressure_coverage(d.pop("coverage"))

        complete = d.pop("complete")

        blocking_scope = check_event_consumer_capacity_pressure_blocking_scope(d.pop("blocking_scope"))

        configured_max_pending_deliveries = d.pop("configured_max_pending_deliveries")

        scopes = []
        _scopes = d.pop("scopes")
        for scopes_item_data in _scopes:
            scopes_item = EventConsumerCapacityScope.from_dict(scopes_item_data)

            scopes.append(scopes_item)

        _capacity_command_budget = d.pop("capacity_command_budget", UNSET)
        capacity_command_budget: EventSubscriptionCapacityCommandBudget | Unset
        if isinstance(_capacity_command_budget, Unset):
            capacity_command_budget = UNSET
        else:
            capacity_command_budget = EventSubscriptionCapacityCommandBudget.from_dict(_capacity_command_budget)

        event_consumer_capacity_pressure = cls(
            plan_max_pending_deliveries=plan_max_pending_deliveries,
            app_id=app_id,
            subscription_id=subscription_id,
            observed_at=observed_at,
            coverage=coverage,
            complete=complete,
            blocking_scope=blocking_scope,
            configured_max_pending_deliveries=configured_max_pending_deliveries,
            scopes=scopes,
            capacity_command_budget=capacity_command_budget,
        )

        return event_consumer_capacity_pressure
