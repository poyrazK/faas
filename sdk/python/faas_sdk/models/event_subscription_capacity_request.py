from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="EventSubscriptionCapacityRequest")


@_attrs_define
class EventSubscriptionCapacityRequest:
    """Explicit per-subscription live-delivery ceiling. Zero restores the plan default; positive values must not exceed the
    current plan ceiling. Requires deploy write or admin scope and MFA.

    """

    max_pending_deliveries: int
    """Maximum pending plus dispatching deliveries; zero restores the current plan default."""

    def to_dict(self) -> dict[str, Any]:
        max_pending_deliveries = self.max_pending_deliveries

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "max_pending_deliveries": max_pending_deliveries,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_pending_deliveries = d.pop("max_pending_deliveries")

        event_subscription_capacity_request = cls(
            max_pending_deliveries=max_pending_deliveries,
        )

        return event_subscription_capacity_request
