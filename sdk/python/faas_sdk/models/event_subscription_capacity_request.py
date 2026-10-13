from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_subscription_capacity_guard import EventSubscriptionCapacityGuard


T = TypeVar("T", bound="EventSubscriptionCapacityRequest")


@_attrs_define
class EventSubscriptionCapacityRequest:
    """Explicit per-subscription live-delivery ceiling. Zero restores the plan default; positive values must not exceed the
    current plan ceiling. Requires deploy write or admin scope and MFA.

    """

    max_pending_deliveries: int
    """Maximum pending plus dispatching deliveries; zero restores the current plan default."""
    expected: EventSubscriptionCapacityGuard | Unset = UNSET
    """Value preconditions checked atomically before changing subscription capacity. Both fields are required.
    Checks saved configuration and the current plan allowance; live usage is not reserved."""

    def to_dict(self) -> dict[str, Any]:
        max_pending_deliveries = self.max_pending_deliveries

        expected: dict[str, Any] | Unset = UNSET
        if not isinstance(self.expected, Unset):
            expected = self.expected.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "max_pending_deliveries": max_pending_deliveries,
            }
        )
        if expected is not UNSET:
            field_dict["expected"] = expected

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_subscription_capacity_guard import EventSubscriptionCapacityGuard

        d = dict(src_dict)
        max_pending_deliveries = d.pop("max_pending_deliveries")

        _expected = d.pop("expected", UNSET)
        expected: EventSubscriptionCapacityGuard | Unset
        if isinstance(_expected, Unset):
            expected = UNSET
        else:
            expected = EventSubscriptionCapacityGuard.from_dict(_expected)

        event_subscription_capacity_request = cls(
            max_pending_deliveries=max_pending_deliveries,
            expected=expected,
        )

        return event_subscription_capacity_request
