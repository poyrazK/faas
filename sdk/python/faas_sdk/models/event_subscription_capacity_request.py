from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

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
    command_id: UUID | Unset = UNSET
    """Optional nonzero command UUID scoped to this owned subscription. Repeat the same value and expectations
    while its 30-day receipt remains retained; repeats return the original receipt without applying again, even
    after later settings or plan changes. Different normalized inputs conflict. No guarantee after expiry or
    deletion."""
    expected: EventSubscriptionCapacityGuard | Unset = UNSET
    """Value preconditions checked atomically before changing subscription capacity. Both fields are required.
    Checks saved configuration and the current plan allowance; live usage is not reserved."""

    def to_dict(self) -> dict[str, Any]:
        max_pending_deliveries = self.max_pending_deliveries

        command_id: str | Unset = UNSET
        if not isinstance(self.command_id, Unset):
            command_id = str(self.command_id)

        expected: dict[str, Any] | Unset = UNSET
        if not isinstance(self.expected, Unset):
            expected = self.expected.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "max_pending_deliveries": max_pending_deliveries,
            }
        )
        if command_id is not UNSET:
            field_dict["command_id"] = command_id
        if expected is not UNSET:
            field_dict["expected"] = expected

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_subscription_capacity_guard import EventSubscriptionCapacityGuard

        d = dict(src_dict)
        max_pending_deliveries = d.pop("max_pending_deliveries")

        _command_id = d.pop("command_id", UNSET)
        command_id: UUID | Unset
        if isinstance(_command_id, Unset):
            command_id = UNSET
        else:
            command_id = UUID(_command_id)

        _expected = d.pop("expected", UNSET)
        expected: EventSubscriptionCapacityGuard | Unset
        if isinstance(_expected, Unset):
            expected = UNSET
        else:
            expected = EventSubscriptionCapacityGuard.from_dict(_expected)

        event_subscription_capacity_request = cls(
            max_pending_deliveries=max_pending_deliveries,
            command_id=command_id,
            expected=expected,
        )

        return event_subscription_capacity_request
