from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="EventSubscriptionCapacityGuard")


@_attrs_define
class EventSubscriptionCapacityGuard:
    """Value preconditions checked atomically before changing subscription capacity. Both fields are required. Checks saved
    configuration and the current plan allowance; live usage is not reserved.

    """

    max_pending_deliveries: int
    """Expected saved setting, including zero for plan default; may exceed a downgraded plan ceiling."""
    plan_max_pending_deliveries: int
    """Expected current plan consumer ceiling before applying a subscription override."""

    def to_dict(self) -> dict[str, Any]:
        max_pending_deliveries = self.max_pending_deliveries

        plan_max_pending_deliveries = self.plan_max_pending_deliveries

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "max_pending_deliveries": max_pending_deliveries,
                "plan_max_pending_deliveries": plan_max_pending_deliveries,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_pending_deliveries = d.pop("max_pending_deliveries")

        plan_max_pending_deliveries = d.pop("plan_max_pending_deliveries")

        event_subscription_capacity_guard = cls(
            max_pending_deliveries=max_pending_deliveries,
            plan_max_pending_deliveries=plan_max_pending_deliveries,
        )

        return event_subscription_capacity_guard
