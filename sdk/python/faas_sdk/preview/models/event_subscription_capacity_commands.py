from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.event_subscription_capacity_commands_outcome import (
    EventSubscriptionCapacityCommandsOutcome,
    check_event_subscription_capacity_commands_outcome,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_subscription_capacity_command_receipt import EventSubscriptionCapacityCommandReceipt


T = TypeVar("T", bound="EventSubscriptionCapacityCommands")


@_attrs_define
class EventSubscriptionCapacityCommands:
    """One fresh page of retained original command evidence. History order does not establish execution order or current
    configuration.

    """

    app_id: UUID
    """Owned application identifier."""
    subscription_id: UUID
    """Owned subscription or retained control identifier."""
    outcome: EventSubscriptionCapacityCommandsOutcome
    """Normalized original-outcome filter for this page."""
    observed_at: datetime.datetime
    """Logical observation time shared by this page."""
    receipts: list[EventSubscriptionCapacityCommandReceipt]
    """Matching unexpired original receipts in descending recorded_at and command UUID order. An accessible empty
    history returns an empty array."""
    has_more: bool
    """Whether another matching retained receipt existed at this observation."""
    next_after: str | Unset = UNSET
    """Opaque scope-bound boundary for the next page. Present only when has_more is true."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        subscription_id = str(self.subscription_id)

        outcome: str = self.outcome

        observed_at = self.observed_at.isoformat()

        receipts = []
        for receipts_item_data in self.receipts:
            receipts_item = receipts_item_data.to_dict()
            receipts.append(receipts_item)

        has_more = self.has_more

        next_after = self.next_after

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "subscription_id": subscription_id,
                "outcome": outcome,
                "observed_at": observed_at,
                "receipts": receipts,
                "has_more": has_more,
            }
        )
        if next_after is not UNSET:
            field_dict["next_after"] = next_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_subscription_capacity_command_receipt import EventSubscriptionCapacityCommandReceipt

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        subscription_id = UUID(d.pop("subscription_id"))

        outcome = check_event_subscription_capacity_commands_outcome(d.pop("outcome"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        receipts = []
        _receipts = d.pop("receipts")
        for receipts_item_data in _receipts:
            receipts_item = EventSubscriptionCapacityCommandReceipt.from_dict(receipts_item_data)

            receipts.append(receipts_item)

        has_more = d.pop("has_more")

        next_after = d.pop("next_after", UNSET)

        event_subscription_capacity_commands = cls(
            app_id=app_id,
            subscription_id=subscription_id,
            outcome=outcome,
            observed_at=observed_at,
            receipts=receipts,
            has_more=has_more,
            next_after=next_after,
        )

        event_subscription_capacity_commands.additional_properties = d
        return event_subscription_capacity_commands

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
