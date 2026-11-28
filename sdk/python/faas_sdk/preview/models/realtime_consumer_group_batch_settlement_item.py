from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_consumer_group_batch_settlement_item_action import (
    RealtimeConsumerGroupBatchSettlementItemAction,
    check_realtime_consumer_group_batch_settlement_item_action,
)
from ..models.realtime_consumer_group_batch_settlement_item_status import (
    RealtimeConsumerGroupBatchSettlementItemStatus,
    check_realtime_consumer_group_batch_settlement_item_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimeConsumerGroupBatchSettlementItem")


@_attrs_define
class RealtimeConsumerGroupBatchSettlementItem:
    """Realtime Consumer Group Batch Settlement Item."""

    sequence: str
    action: RealtimeConsumerGroupBatchSettlementItemAction
    status: RealtimeConsumerGroupBatchSettlementItemStatus
    available_at: datetime.datetime | Unset = UNSET
    lease_until: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sequence = self.sequence

        action: str = self.action

        status: str = self.status

        available_at: str | Unset = UNSET
        if not isinstance(self.available_at, Unset):
            available_at = self.available_at.isoformat()

        lease_until: str | Unset = UNSET
        if not isinstance(self.lease_until, Unset):
            lease_until = self.lease_until.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sequence": sequence,
                "action": action,
                "status": status,
            }
        )
        if available_at is not UNSET:
            field_dict["available_at"] = available_at
        if lease_until is not UNSET:
            field_dict["lease_until"] = lease_until

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sequence = d.pop("sequence")

        action = check_realtime_consumer_group_batch_settlement_item_action(d.pop("action"))

        status = check_realtime_consumer_group_batch_settlement_item_status(d.pop("status"))

        _available_at = d.pop("available_at", UNSET)
        available_at: datetime.datetime | Unset
        if isinstance(_available_at, Unset):
            available_at = UNSET
        else:
            available_at = datetime.datetime.fromisoformat(_available_at)

        _lease_until = d.pop("lease_until", UNSET)
        lease_until: datetime.datetime | Unset
        if isinstance(_lease_until, Unset):
            lease_until = UNSET
        else:
            lease_until = datetime.datetime.fromisoformat(_lease_until)

        realtime_consumer_group_batch_settlement_item = cls(
            sequence=sequence,
            action=action,
            status=status,
            available_at=available_at,
            lease_until=lease_until,
        )

        realtime_consumer_group_batch_settlement_item.additional_properties = d
        return realtime_consumer_group_batch_settlement_item

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
