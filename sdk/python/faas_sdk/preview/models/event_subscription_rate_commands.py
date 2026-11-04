from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_subscription_rate_command_receipt import EventSubscriptionRateCommandReceipt


T = TypeVar("T", bound="EventSubscriptionRateCommands")


@_attrs_define
class EventSubscriptionRateCommands:
    """Bounded retained rate command history in increasing sequence order; expiry can leave sequence gaps. Pages are fresh
    observations.

    """

    receipts: list[EventSubscriptionRateCommandReceipt]
    next_after: int | Unset = UNSET
    """Exclusive cursor for the next page when another retained receipt exists."""

    def to_dict(self) -> dict[str, Any]:
        receipts = []
        for receipts_item_data in self.receipts:
            receipts_item = receipts_item_data.to_dict()
            receipts.append(receipts_item)

        next_after = self.next_after

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "receipts": receipts,
            }
        )
        if next_after is not UNSET:
            field_dict["next_after"] = next_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_subscription_rate_command_receipt import EventSubscriptionRateCommandReceipt

        d = dict(src_dict)
        receipts = []
        _receipts = d.pop("receipts")
        for receipts_item_data in _receipts:
            receipts_item = EventSubscriptionRateCommandReceipt.from_dict(receipts_item_data)

            receipts.append(receipts_item)

        next_after = d.pop("next_after", UNSET)

        event_subscription_rate_commands = cls(
            receipts=receipts,
            next_after=next_after,
        )

        return event_subscription_rate_commands
