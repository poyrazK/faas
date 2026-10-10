from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_recovery_preflight_item_capacity_scope import (
    EventRecoveryPreflightItemCapacityScope,
    check_event_recovery_preflight_item_capacity_scope,
)
from ..models.event_recovery_preflight_item_reason import (
    EventRecoveryPreflightItemReason,
    check_event_recovery_preflight_item_reason,
)
from ..models.event_recovery_preflight_item_status import (
    EventRecoveryPreflightItemStatus,
    check_event_recovery_preflight_item_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="EventRecoveryPreflightItem")


@_attrs_define
class EventRecoveryPreflightItem:
    """Sampled pending recovery item eligibility and current wait or skip diagnostics."""

    position: int
    status: EventRecoveryPreflightItemStatus
    reason: EventRecoveryPreflightItemReason
    receipt_retain_until: datetime.datetime | Unset = UNSET
    """Nominal receipt retention boundary when routing has settled."""
    receipt_retention_held: bool | Unset = UNSET
    """Current backfill or recovery hold; ends when its owning job or item releases it."""
    capacity_scope: EventRecoveryPreflightItemCapacityScope | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        position = self.position

        status: str = self.status

        reason: str = self.reason

        receipt_retain_until: str | Unset = UNSET
        if not isinstance(self.receipt_retain_until, Unset):
            receipt_retain_until = self.receipt_retain_until.isoformat()

        receipt_retention_held = self.receipt_retention_held

        capacity_scope: str | Unset = UNSET
        if not isinstance(self.capacity_scope, Unset):
            capacity_scope = self.capacity_scope

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "position": position,
                "status": status,
                "reason": reason,
            }
        )
        if receipt_retain_until is not UNSET:
            field_dict["receipt_retain_until"] = receipt_retain_until
        if receipt_retention_held is not UNSET:
            field_dict["receipt_retention_held"] = receipt_retention_held
        if capacity_scope is not UNSET:
            field_dict["capacity_scope"] = capacity_scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        position = d.pop("position")

        status = check_event_recovery_preflight_item_status(d.pop("status"))

        reason = check_event_recovery_preflight_item_reason(d.pop("reason"))

        _receipt_retain_until = d.pop("receipt_retain_until", UNSET)
        receipt_retain_until: datetime.datetime | Unset
        if isinstance(_receipt_retain_until, Unset):
            receipt_retain_until = UNSET
        else:
            receipt_retain_until = datetime.datetime.fromisoformat(_receipt_retain_until)

        receipt_retention_held = d.pop("receipt_retention_held", UNSET)

        _capacity_scope = d.pop("capacity_scope", UNSET)
        capacity_scope: EventRecoveryPreflightItemCapacityScope | Unset
        if isinstance(_capacity_scope, Unset):
            capacity_scope = UNSET
        else:
            capacity_scope = check_event_recovery_preflight_item_capacity_scope(_capacity_scope)

        event_recovery_preflight_item = cls(
            position=position,
            status=status,
            reason=reason,
            receipt_retain_until=receipt_retain_until,
            receipt_retention_held=receipt_retention_held,
            capacity_scope=capacity_scope,
        )

        return event_recovery_preflight_item
